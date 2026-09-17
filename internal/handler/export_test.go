package handler

// WaitPasswordResetSends blocks until every password-reset send started so far has
// finished. RequestPasswordReset answers before it knows whether the account exists, so
// a test asserting that nothing was minted for an unknown or ineligible address has to
// wait for the background work to end rather than poll for a row that should never
// appear.
func (h *Handler) WaitPasswordResetSends() {
	h.resetSends.Wait()
}
