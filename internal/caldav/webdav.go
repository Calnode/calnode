package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
func (c *Client) propfind(ctx context.Context, rawURL, username, password, depth, body string) (*msMultistatus, string, error) {
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
			cur = resolveRef(cur, loc)
			continue
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, cur, fmt.Errorf("caldav: authentication failed (status %d) — check the username and app password", status)
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
