package editorintelligence

import (
	"encoding/json"
	"testing"
)

// The answers are intelephense's own, recorded against a fixture project:
// the hover result of each position as the server sent it.
func TestPHPClassNameReadsIntelephenseHovers(t *testing.T) {
	cases := []struct {
		name  string
		hover string
		want  string
	}{
		{"a declaration, an import, an alias and a use alike", `{"contents":{"kind":"markdown","value":"__Foo\\\\Bar__\n\nA docblock.\n\n` + "```php\\n<?php\\nclass Bar { }\\n```" + `"}}`, `Foo\Bar`},
		{"the global namespace", `{"contents":{"kind":"markdown","value":"__GlobalThing__\n\n` + "```php\\n<?php\\nclass GlobalThing { }\\n```" + `"}}`, `GlobalThing`},
		{"new appends the constructor below a rule", `{"contents":{"kind":"markdown","value":"__DateTime__\n\nRepresentation of date and time.\n\n` + "```php\\n<?php\\nclass DateTime implements DateTimeInterface { }\\n```" + `\n\n_@link_ <https://php.net/manual/en/class.datetime.php>\n\n---\n\n__DateTime::\\_\\_construct__\n\n` + "```php\\n<?php\\npublic function __construct(string $datetime = 'now') { }\\n```" + `"}}`, `DateTime`},
		{"an attribute with its constructor", `{"contents":{"kind":"markdown","value":"__Attribute__\n\n` + "```php\\n<?php\\nfinal class Attribute { }\\n```" + `\n\n_@since_ 8.0\n\n---\n\n__Attribute::\\_\\_construct__\n\n` + "```php\\n<?php\\npublic function __construct(int $flags = Attribute::TARGET_ALL) { }\\n```" + `"}}`, `Attribute`},
		{"modifiers and a body", `{"contents":{"kind":"markdown","value":"__App\\\\User__\n\nException is the base class for\nall Exceptions.\n\n` + "```php\\n<?php\\nfinal class User extends Exception implements Shape\\n{\\n    use Sized;\\n}\\n```" + `"}}`, `App\User`},
		{"an abstract class", `{"contents":{"kind":"markdown","value":"__App\\\\Edge__\n\n` + "```php\\n<?php\\nabstract class Edge { }\\n```" + `"}}`, `App\Edge`},
		{"an interface", `{"contents":{"kind":"markdown","value":"__Foo\\\\Shape__\n\n` + "```php\\n<?php\\ninterface Shape { }\\n```" + `"}}`, `Foo\Shape`},
		{"a trait", `{"contents":{"kind":"markdown","value":"__Foo\\\\Sized__\n\n` + "```php\\n<?php\\ntrait Sized { }\\n```" + `"}}`, `Foo\Sized`},
		{"a backed enum", `{"contents":{"kind":"markdown","value":"__Foo\\\\Suit__\n\n` + "```php\\n<?php\\nenum Suit: string\\n{\\n    case Hearts;\\n}\\n```" + `"}}`, `Foo\Suit`},
		{"a namespaced function", `{"contents":{"kind":"markdown","value":"__App\\\\helper__\n\n` + "```php\\n<?php\\nfunction helper(): void { }\\n```" + `\n\n_@return_ ` + "`void`" + `"}}`, ""},
		{"a global function", `{"contents":{"kind":"markdown","value":"__strlen__\n\nGet string length\n\n` + "```php\\n<?php\\nfunction strlen(string $string): int { }\\n```" + `"}}`, ""},
		{"a method", `{"contents":{"kind":"markdown","value":"__Foo\\\\Bar::run__\n\n` + "```php\\n<?php\\npublic function run(): void { }\\n```" + `\n\n_@return_ ` + "`void`" + `"}}`, ""},
		{"a class constant", `{"contents":{"kind":"markdown","value":"__Foo\\\\Bar::X__\n\n` + "```php\\n<?php\\npublic const X = 1;\\n```" + `\n\n_@var_ ` + "`1`" + `"}}`, ""},
		{"an enum case", `{"contents":{"kind":"markdown","value":"__Foo\\\\Suit::Hearts__\n\n` + "```php\\n<?php\\ncase Hearts = 'h';\\n```" + `"}}`, ""},
		{"a variable", `{"contents":{"kind":"markdown","value":"_@var_ ` + "`\\\\Foo\\\\Bar $x`" + `"}}`, ""},
		{"no hover", `null`, ""},
		{"a declaration of another name", `{"contents":{"kind":"markdown","value":"__Foo\\\\Bar__\n\n` + "```php\\n<?php\\nclass Other { }\\n```" + `"}}`, ""},
		{"a title without a declaration", `{"contents":{"kind":"markdown","value":"__Foo\\\\Bar__"}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, _, err := hoverText(json.RawMessage(tc.hover))
			if err != nil {
				t.Fatal(err)
			}
			if got := phpClassName(text); got != tc.want {
				t.Fatalf("got %q, want %q from %q", got, tc.want, text)
			}
		})
	}
}

func TestHoverTextReadsEveryContentShape(t *testing.T) {
	cases := map[string]string{
		`{"contents":{"kind":"markdown","value":"__A__"}}`:                "__A__",
		`{"contents":"plain"}`:                                            "plain",
		`{"contents":{"language":"php","value":"class A { }"}}`:           "```php\nclass A { }\n```",
		`{"contents":["__A__",{"language":"php","value":"class A { }"}]}`: "__A__\n\n```php\nclass A { }\n```",
		`{"contents":[]}`: "",
		`null`:            "",
		`{"contents":{"kind":"plaintext","value":"class A"},"range":{"start":{}}}`: "class A",
	}
	for raw, want := range cases {
		got, _, err := hoverText(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if got != want {
			t.Fatalf("%s: got %q, want %q", raw, got, want)
		}
	}
}

func TestHoverTextKeepsTheSymbolsRange(t *testing.T) {
	_, span, err := hoverText(json.RawMessage(`{"contents":{"kind":"markdown","value":"__Foo\\\\Bar__"},"range":{"start":{"line":4,"character":4},"end":{"line":4,"character":11}}}`))
	if err != nil || span == nil || *span != (lspRange{Start: lspPosition{Line: 4, Character: 4}, End: lspPosition{Line: 4, Character: 11}}) {
		t.Fatalf("range %+v %v", span, err)
	}
	_, span, err = hoverText(json.RawMessage(`{"contents":"plain"}`))
	if err != nil || span != nil {
		t.Fatalf("no range named, none answered: %+v %v", span, err)
	}
}
