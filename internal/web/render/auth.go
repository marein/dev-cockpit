package render

// LoginData is the model for the login page. It shows one way in and links to
// the other one below it.
type LoginData struct {
	Page
	Next string
	// Passkey renders the passkey button instead of the username and password
	// mask. It is what the page opens with wherever a passkey is registered for
	// the host.
	Passkey bool
	// PasswordURL is the way from the passkey button to the mask, PasskeyURL
	// the way back. Exactly one of them is set, and only while a passkey is
	// registered for this host.
	PasswordURL string
	PasskeyURL  string
}
