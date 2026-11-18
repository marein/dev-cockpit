package render

type SettingsBrowserData struct {
	Page
	SettingsNav SettingsNav
	Tokens      []BrowserExtensionToken
	TokensError string
	NameMax     int
	Version     string
}

type BrowserExtensionToken struct {
	ID      string
	Name    string
	Created string
	Used    string
}
