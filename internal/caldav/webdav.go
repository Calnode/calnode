package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/calnode/calnode/internal/calendar"
)

// do issues one WebDAV request with HTTP Basic auth and returns the status, body, and any
// Location header (for manual redirect following). The shared http.Client is configured (in
// New) NOT to auto-follow redirects, because CalDAV discovery must re-issue the SAME method
// (PROPFIND/REPORT) on a 301/302 — Go's default redirect handling would downgrade to GET.
func (c *Client) do(ctx context.Context, method, rawURL, username, password, depth, body string) (status int, respBody []byte, location string, err error) {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return 0, nil, "", err
	}
	req.SetBasicAuth(username, password)
	if body != "" {
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	}
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, "", err
	}
	return resp.StatusCode, b, resp.Header.Get("Location"), nil
}

// propfind issues a PROPFIND, following up to 5 redirects with the method preserved, and
// returns the parsed multistatus. A non-2xx terminal status is an error.
//
// pinOrigin, when set, is a URL whose origin every redirect must stay on: a redirect elsewhere
// is refused before anything is sent to it, so the account's credentials cannot follow it.
// Listing an existing account's calendars pins to the connected calendar. Connect-time
// discovery passes "" and is unchanged: it starts from the URL the user typed, and discovery
// from there can legitimately cross hosts (iCloud's calendar home is on a per-account partition
// host, and a /.well-known/caldav redirect may point anywhere).
func (c *Client) propfind(ctx context.Context, pinOrigin, rawURL, username, password, depth, body string) (*msMultistatus, string, error) {
	cur := rawURL
	for hop := 0; hop < 6; hop++ {
		status, b, loc, err := c.do(ctx, "PROPFIND", cur, username, password, depth, body)
		if err != nil {
			return nil, cur, err
		}
		if status == http.StatusMovedPermanently || status == http.StatusFound ||
			status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect {
			if loc == "" {
				return nil, cur, fmt.Errorf("caldav: redirect without Location from %s", cur)
			}
			next := resolveRef(cur, loc)
			if pinOrigin != "" && !sameOrigin(next, pinOrigin) {
				return nil, cur, fmt.Errorf("caldav: refusing to follow a redirect from %s to another origin (%s)", cur, displayOrigin(next))
			}
			cur = next
			continue
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, cur, &authError{status: status}
		}
		if status != http.StatusMultiStatus && status != http.StatusOK {
			return nil, cur, fmt.Errorf("caldav: PROPFIND %s returned status %d", cur, status)
		}
		var ms msMultistatus
		if err := xml.Unmarshal(b, &ms); err != nil {
			return nil, cur, fmt.Errorf("caldav: parse PROPFIND response: %w", err)
		}
		return &ms, cur, nil
	}
	return nil, cur, fmt.Errorf("caldav: too many redirects resolving %s", rawURL)
}

// authError is a 401/403 from the server. Its message is what the connect form shows. It also
// unwraps to calendar.ErrReauthRequired, because listing an existing account's calendars now
// talks to the server: an app password that has since been revoked must read as "reconnect
// this account", not as the server being unreachable.
type authError struct{ status int }

func (e *authError) Error() string {
	return fmt.Sprintf("caldav: authentication failed (status %d) — check the username and app password", e.status)
}

func (e *authError) Unwrap() error { return calendar.ErrReauthRequired }

// resolveRef resolves a (possibly relative) href against a base request URL, returning an
// absolute URL string. On parse failure it returns the ref unchanged.
func resolveRef(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

// sameOrigin reports whether two absolute URLs share a scheme, host and port. An omitted port
// equals the scheme's default, so https://h and https://h:443 are the same origin. A URL that
// does not parse, or has no scheme or host, matches nothing.
func sameOrigin(a, b string) bool {
	oa, ob := origin(a), origin(b)
	return oa != "" && oa == ob
}

func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname())
	if scheme == "" || host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

// displayOrigin renders a URL's origin as an operator would type it (https://host, or
// http://host:5000), for error messages.
func displayOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host
}

// lastSegment returns the final path segment of a collection URL ("work" for .../user/work/),
// used as a calendar's name when the server reports no display name.
func lastSegment(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	seg := path.Base(strings.TrimRight(u.Path, "/"))
	if seg == "" || seg == "." || seg == "/" {
		return raw
	}
	return seg
}

// parentCollection returns the URL of the collection that contains raw (.../user/ for
// .../user/work/).
func parentCollection(raw string) string {
	return resolveRef(strings.TrimRight(raw, "/"), "./")
}

// ----- WebDAV / CalDAV multistatus XML -----

type msMultistatus struct {
	XMLName   xml.Name     `xml:"DAV: multistatus"`
	Responses []msResponse `xml:"DAV: response"`
}

type msResponse struct {
	Href      string       `xml:"DAV: href"`
	Propstats []msPropstat `xml:"DAV: propstat"`
}

type msPropstat struct {
	Status string `xml:"DAV: status"`
	Prop   msProp `xml:"DAV: prop"`
}

type msProp struct {
	CurrentUserPrincipal hrefHolder       `xml:"DAV: current-user-principal"`
	CalendarHomeSet      hrefHolder       `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set"`
	DisplayName          string           `xml:"DAV: displayname"`
	ResourceType         resourceType     `xml:"DAV: resourcetype"`
	SupportedComps       supportedCompSet `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set"`
	CalendarData         string           `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
	// nil when the server did not report the property (or reported it under a non-2xx status).
	PrivilegeSet *privilegeSet `xml:"DAV: current-user-privilege-set"`
}

// privilegeSet is DAV:current-user-privilege-set (RFC 3744 §5.4), the privileges the signed-in
// user holds on a resource. Only the ones that allow adding an event are decoded.
type privilegeSet struct {
	Privileges []privilege `xml:"DAV: privilege"`
}

type privilege struct {
	All          *struct{} `xml:"DAV: all"`
	Write        *struct{} `xml:"DAV: write"`
	WriteContent *struct{} `xml:"DAV: write-content"`
	Bind         *struct{} `xml:"DAV: bind"`
}

// canWrite reports whether events can be created in the collection. A set the server did not
// report counts as writable: that is what every CalDAV calendar was assumed to be before
// privileges were read, and a server that omits the property has given no reason to refuse.
// write-properties alone does not count, since servers grant it on read-only shares so the
// sharee can rename or recolour them.
func (s *privilegeSet) canWrite() bool {
	if s == nil {
		return true
	}
	for _, p := range s.Privileges {
		if p.All != nil || p.Write != nil || p.WriteContent != nil || p.Bind != nil {
			return true
		}
	}
	return false
}

type hrefHolder struct {
	Href string `xml:"DAV: href"`
}

// resourceType carries the child element names; a calendar collection has a <C:calendar/> child.
type resourceType struct {
	Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
}

type supportedCompSet struct {
	Comps []supportedComp `xml:"urn:ietf:params:xml:ns:caldav comp"`
}

type supportedComp struct {
	Name string `xml:"name,attr"`
}

// okProp returns the prop from the first propstat whose status is 2xx (HTTP "... 200 OK").
func (r msResponse) okProp() msProp {
	for _, ps := range r.Propstats {
		if strings.Contains(ps.Status, " 200 ") || strings.Contains(ps.Status, " 207 ") {
			return ps.Prop
		}
	}
	if len(r.Propstats) > 0 {
		return r.Propstats[0].Prop
	}
	return msProp{}
}
