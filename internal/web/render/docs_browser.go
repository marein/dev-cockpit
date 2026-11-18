package render

func docsBrowserTopic() DocsTopic {
	return DocsTopic{
		Key:      "browser",
		Title:    "Browser extension",
		Icon:     "ti-puzzle",
		Lead:     "Pick elements, collect the console and draw on pages in your own browser, then send it to an assistant or a coder.",
		Intro:    `Settings &rarr; Browser offers the extension as a zip for Chrome, Firefox and Safari. Its version changes only with its own code. <b>The extension is not signed, signing can be added if it is wanted.</b>`,
		LinkURL:  "/settings/browser",
		LinkText: "Open settings",
		Items: []DocsItem{
			{
				Title: "Chrome and Edge",
				Desc:  `Version 116 or later. Unzip the zip into a folder, switch on <em>Developer mode</em> in <code>chrome://extensions</code> (Edge: <code>edge://extensions</code>) and <em>Load unpacked</em> that folder. To update, unzip the new zip into the same folder and press the extension's reload button there. The panel opens in the browser's side panel. The key is set in <code>chrome://extensions/shortcuts</code> (Edge: <code>edge://extensions/shortcuts</code>).`,
			},
			{
				Title: "Firefox",
				Desc:  `Version 142 or later. <em>Load Temporary Add-on…</em> in <code>about:debugging#/runtime/this-firefox</code> with the zip, it stays until Firefox quits. ESR, Developer Edition and Nightly keep it: switch off <code>xpinstall.signatures.required</code> in <code>about:config</code>, then <em>Install Add-on From File…</em> in <code>about:addons</code> with the zip. Release Firefox keeps only signed add-ons. To update, load or install the new zip the same way. The panel opens in Firefox's sidebar. The key is set under <em>Manage Extension Shortcuts</em> in <code>about:addons</code>.`,
			},
			{
				Title: "Safari",
				Desc:  `Version 26 or later on the Mac. Tick <em>Show features for web developers</em> in Safari &rarr; Settings &rarr; Advanced and <em>Allow unsigned extensions</em> in Settings &rarr; Developer, choose <em>Add Temporary Extension…</em> there with the zip or its folder, then tick the extension in Settings &rarr; Extensions. Safari drops it after 24 hours or when it quits, and forgets <em>Allow unsigned extensions</em> when it quits. To update, add the new zip the same way. Safari asks for each website through the extension's button, allow it on every website. Safari has no side panel: the button and the key open the panel as a popup, <em>Pick</em> there starts picking, a click into the page closes the popup, picking goes on and the tray stays.`,
			},
			{
				Title: "Setup",
				Desc:  `The setup opens after installing, later through <em>Setup</em> in the panel, and takes the cockpit's address and a token. <em>Save and check</em> asks the browser for access to that address, and to every page while <em>Access to every page</em> is ticked, then checks address and token. It says when nothing answers, the address redirects, the token is wrong or revoked, or the browser does not trust the certificate. Trust a self signed certificate in a tab of that browser and check again. With access to every page, picking and drawing work in every tab, picking goes on after a reload or another page, and the panel lists the console. Without it, the extension reaches a tab only after a click on its button or its key there, again after the tab opens another site. The browser's settings of the extension withdraw the access, the setup never drops it. When the cockpit offers a newer version, the panel and the setup name it with <em>Download</em>, the old one keeps working. When the extension is too old or too new for the cockpit, they say what to install and <em>Send</em> stays off. An update keeps each tab's tray and console list, it stops picking and removes drawings in pages already open.`,
			},
			{
				Title: "Pick",
				Keys:  []DocsKeys{{Caps: []string{"Alt", "Shift", "C"}}},
				Desc:  `The extension's button or its key opens the panel and starts picking in the tab, a second press ends it. <em>Pick</em> in the panel starts it too. The element under the pointer gets an outline with tag, id, classes and size, a click picks it and does not reach the page. <em>Parent</em> and <em>Child</em> widen or narrow the pick, <kbd>Escape</kbd> ends picking. Closing the panel ends picking and drawing in every tab of its window. The panel holds the tray of the tab shown: up to eight picked elements as chips, a note and what its last send or copy said. A chip outlines its element on hover and is dashed when the element is gone. The tray stays through reloads and other pages until it is sent or cleared, and ends with the tab. <em>Send</em> lists <em>New assistant</em>, the assistants and the running coders under a filter: typing narrows them, the arrows move, <kbd>Enter</kbd> sends and <kbd>Escape</kbd> empties the filter, then closes. <em>Copy</em> copies the same Markdown. With several coders installed, a <em>New with</em> row per coder names the one the new assistant runs on. A new assistant is named after the note's first line, else the page's title, else its address.`,
			},
			{
				Title: "Console and drawing",
				Desc:  `The panel lists the console of the tab shown on http and https pages, from the page's first script on: console errors and warnings, uncaught errors, unhandled rejections and failed requests (network errors, status 400 and up). A page loaded before access to every page or before an update of the extension is caught after a reload, the panel says so. The badge on the button counts them for the current page. Another page leaves a mark in the list, a reload clears it. A row unfolds its stack, <em>+</em> adds it to the tray, <em>Add all</em> adds the newest 60 as one chip. The list keeps a page's first 200 entries and counts the rest. <em>Draw</em> draws on the page with pen, arrow, box and text in five colors, <kbd>Ctrl</kbd>+<kbd>Z</kbd> and <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>Z</kbd> undo and redo, <kbd>Escape</kbd> stops. While drawing the page neither scrolls nor takes a click. The drawing stays until <em>Clear</em>, a reload, another page or a send, which ends picking and drawing in its tab. Picking and drawing take turns. <em>Download PNG</em> saves the visible part of the page with the drawing at the screen's pixel ratio, <em>Add to the tray</em> adds it as a chip, a click on the chip shows it. An assistant gets the picture as an image of the message, a coder as a file, and the text names the element under every mark.`,
			},
			{
				Title: "What the extension may do",
				Desc:  `<code>&lt;all_urls&gt;</code> is every page, asked at setup while <em>Access to every page</em> is ticked. Chrome needs all of it, not only http and https, to take a picture of a tab without a click on the button. <code>activeTab</code> reaches a tab after a click on the button or the key there. <code>scripting</code> puts the picker, the drawing and the console catch into pages. <code>storage</code> keeps the trays and console lists through an update until the browser starts again, and the drawing tools last chosen. <code>sidePanel</code> is the panel in Chrome. <code>webRequest</code>, for the cockpit's address alone, tells an untrusted certificate from an address that does not answer. Firefox and Safari get the cockpit's host on every port, since Firefox's access names no port and Safari documents none. The console catch in every page holds nothing of the extension, and the page can forge what it reports. The picker and the drawing enter a page only while you pick or draw there or point at a chip of its tray, and take only your own clicks and keys. The token stays in the extension's own storage, out of every page's reach, and goes in the <code>Authorization</code> header to the cockpit's address alone, never after a redirect and never with a cookie. A page cannot start picking or drawing, fill the tray or send: the tray, the console and <em>Send</em> stand in the browser's panel and show page text as text. The extension loads nothing from the internet and opens none of its files to websites.`,
			},
			{
				Title: "Tokens for the browser extension",
				Desc:  `Settings &rarr; Browser makes the tokens, one per browser, named after it. <em>New token</em> asks for a name and shows the token once with <em>Copy</em>, the cockpit keeps only its hash. The list shows when each token was made and last used, updated at most once a minute. <em>Revoke</em> asks first, the token's next request is refused. A token lists the assistants and the running coders, starts new assistants and sends to them. It opens no page of the cockpit, and the cockpit's login opens none of its routes. Up to 20 tokens. Only you make, see and revoke them in the browser, never an assistant or a shared link. Tokens stay out of a backup.`,
			},
			{
				Title: "The card in the thread",
				Desc:  `In the assistant's thread a send shows your note with a folded card, marked <em>From the extension</em> with the token's name: the page's title, address and viewport and a chip each for the elements, the console entries and the drawings. A tap unfolds per element its selector, text, box, font, colors, styles and HTML, the console entries and the picture. <em>Copy as Markdown</em> copies the whole message. The assistant reads the Markdown, the card is only how the thread shows it.`,
			},
			{
				Title: "The extension's API",
				Desc:  `Every call carries the token in <code>Authorization: Bearer &lt;token&gt;</code>, never in an address, and answers JSON. <code>GET /api/browser-extension/version</code> answers the extension version the cockpit offers in <code>extension</code> and the API versions it speaks in <code>api</code>. This answer never changes its shape, so every extension tells whether it fits. <code>GET /api/browser-extension/targets</code> lists the assistants with id and name, the coders a new assistant can run on in <code>assistantCoders</code> with id and name and the running coders with id, name and project. <code>POST /api/browser-extension/assistants</code> starts a new assistant on the coder in <code>coder</code>. <code>POST /api/browser-extension/pictures</code> takes a PNG of at most 32 MB and 8192 pixels a side in <code>image</code>, for <code>to</code> (<code>assistant</code> or <code>coder</code>) and <code>id</code>, and answers the <code>file</code> the message names. <code>POST /api/browser-extension/send</code> sends <code>message</code>, the Markdown, to <code>to</code> and <code>id</code>, to an assistant with the <code>card</code> and a <code>picture</code> field per picture of the card. A coder gets the text typed in, control characters shown as text. A refusal answers 4xx with <code>{"error": "…"}</code>, a wrong or revoked token 401. Other origins get no CORS answer.`,
			},
		},
	}
}
