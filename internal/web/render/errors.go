package render

// ErrorPage is the model for the standalone HTTP error page. It deliberately
// avoids the shared Page model so it can be rendered from anywhere (including a
// panic recovery) without touching the session, CSRF token, or flash store.
type ErrorPage struct {
	Title   string // document <title>, consumed by html_head.gohtml
	Status  int
	Heading string
	Message string

	// CSRFToken and Jingle stay empty. html_head.gohtml evaluates both on
	// every page, a model without the fields fails the whole error render.
	// TestErrorPageRenders guards this contract.
	CSRFToken string
	Jingle    string
}
