package editorintelligence

import (
	"strings"
	"testing"
)

// phpSources are the files the PHP rule was read against intelephense
// 1.18.5 on: every way a clause names a type, spanning lines, behind
// comments, aliased, fully qualified, behind an aliased namespace and
// relative to the current one, an enum, an anonymous class, and usages of
// the types that are no clause.
var phpSources = map[string]string{
	"Anon.php":      "<?php\nnamespace App\\Geo;\n\nfunction make(): Shape\n{\n    return new class(1, 2) extends Base implements Shape {\n        public function area(): float { return 2.0; }\n    };\n}\n",
	"Base.php":      "<?php\nnamespace App\\Geo;\n\nabstract class Base implements Shape, \\JsonSerializable\n{\n}\n",
	"Circle.php":    "<?php\nnamespace App\\Round;\n\nuse App\\Geo\\Base as GeoBase;\nuse App\\Geo\\Shape as Form;\n\nclass Circle extends GeoBase implements Form\n{\n}\n",
	"Commented.php": "<?php\nnamespace App\\Geo;\n\n#[\\Attribute]\nCLASS Commented EXTENDS /* the base { */ base implements // shape {\n    Shape\n{\n}\n",
	"Doc.php":       "<?php\nnamespace App\\Geo;\n\n/**\n * A documented one.\n */\n#[\\Attribute(\\Attribute::TARGET_CLASS)]\n#[\\AllowDynamicProperties]\nfinal class Doc extends Base implements \\Countable\n{\n}\n",
	"Fq.php":        "<?php\nnamespace App\\Other;\n\nclass Fq\n    extends \\App\\Geo\\Base\n    implements\n        \\App\\Geo\\Shape\n{\n}\n",
	"Grouped.php":   "<?php\nnamespace App\\Grp;\n\nuse App\\Geo\\{Shape, Base as B};\n\nclass Grouped extends B implements Shape { public function area(): float { return 0.0; } }\ninterface Wide extends Shape, \\Stringable {}\n",
	"Multi.php":     "<?php\nnamespace App\\One {\n    use App\\Geo\\Base as X;\n    class InOne extends X {}\n}\n",
	"NsAlias.php":   "<?php\nnamespace App\\Ns;\n\nuse App\\Geo as G;\nuse App\\Geo;\n\nclass NsAlias extends G\\Base {}\nclass NsPlain extends Geo\\Base {}\n",
	"Rel.php":       "<?php\nnamespace App;\n\nclass Rel extends namespace\\Geo\\Base\n{\n}\n",
	"Shape.php":     "<?php\nnamespace App\\Geo;\n\ninterface Shape\n{\n    public function area(): float;\n}\n",
	"Solid.php":     "<?php\nnamespace App\\Geo;\n\ninterface Solid extends\n    \\Countable,\n    Shape\n{\n}\n",
	"Square.php":    "<?php\nnamespace App\\Geo;\n\nfinal class Square implements Shape\n{\n}\n",
	"Suit.php":      "<?php\nnamespace App\\Geo;\n\nenum Suit: string implements Shape\n{\n    case Hearts = 'H';\n}\n",
	"User.php":      "<?php\nnamespace App\\Usage;\n\nuse App\\Geo\\Shape;\n\nclass User\n{\n    public function take(Shape $s): Shape { return $s; }\n    public function check($x): bool { return $x instanceof Shape || $x::class === Shape::class; }\n    public function class(): string { return Shape::class; }\n}\n",
}

func phpSourceLine(file string) func(n int) (string, bool) {
	rows := strings.Split(phpSources[file], "\n")
	return func(n int) (string, bool) {
		if n < 1 || n > len(rows) {
			return "", false
		}
		return rows[n-1], true
	}
}

// The locations are the references intelephense answered for Shape, Base
// and the aliases, 1-based like the service maps them.
func TestPHPClauseName(t *testing.T) {
	for _, tc := range []struct {
		file      string
		line, col int
		want      belowType
		ok        bool
	}{
		{"Anon.php", 6, 51, belowType{Location{Path: "Anon.php", Line: 6, Character: 15}, groupImplementations}, true},
		{"Anon.php", 6, 35, belowType{Location{Path: "Anon.php", Line: 6, Character: 15}, groupSubclasses}, true},
		{"Base.php", 4, 31, belowType{Location{Path: "Base.php", Line: 4, Character: 15}, groupImplementations}, true},
		{"Commented.php", 6, 4, belowType{Location{Path: "Commented.php", Line: 5, Character: 6}, groupImplementations}, true},
		{"Commented.php", 5, 41, belowType{Location{Path: "Commented.php", Line: 5, Character: 6}, groupSubclasses}, true},
		{"Doc.php", 9, 24, belowType{Location{Path: "Doc.php", Line: 9, Character: 12}, groupSubclasses}, true},
		{"Doc.php", 9, 40, belowType{Location{Path: "Doc.php", Line: 9, Character: 12}, groupImplementations}, true},
		{"Fq.php", 7, 8, belowType{Location{Path: "Fq.php", Line: 4, Character: 6}, groupImplementations}, true},
		{"Fq.php", 5, 12, belowType{Location{Path: "Fq.php", Line: 4, Character: 6}, groupSubclasses}, true},
		{"Fq.php", 5, 20, belowType{Location{Path: "Fq.php", Line: 4, Character: 6}, groupSubclasses}, true},
		{"Grouped.php", 6, 35, belowType{Location{Path: "Grouped.php", Line: 6, Character: 6}, groupImplementations}, true},
		{"Grouped.php", 6, 22, belowType{Location{Path: "Grouped.php", Line: 6, Character: 6}, groupSubclasses}, true},
		{"Grouped.php", 7, 23, belowType{Location{Path: "Grouped.php", Line: 7, Character: 10}, groupImplementations}, true},
		{"Circle.php", 7, 21, belowType{Location{Path: "Circle.php", Line: 7, Character: 6}, groupSubclasses}, true},
		{"Circle.php", 7, 40, belowType{Location{Path: "Circle.php", Line: 7, Character: 6}, groupImplementations}, true},
		{"Multi.php", 4, 24, belowType{Location{Path: "Multi.php", Line: 4, Character: 10}, groupSubclasses}, true},
		{"NsAlias.php", 7, 22, belowType{Location{Path: "NsAlias.php", Line: 7, Character: 6}, groupSubclasses}, true},
		{"NsAlias.php", 8, 22, belowType{Location{Path: "NsAlias.php", Line: 8, Character: 6}, groupSubclasses}, true},
		{"Rel.php", 4, 18, belowType{Location{Path: "Rel.php", Line: 4, Character: 6}, groupSubclasses}, true},
		{"Solid.php", 6, 4, belowType{Location{Path: "Solid.php", Line: 4, Character: 10}, groupImplementations}, true},
		{"Solid.php", 5, 4, belowType{Location{Path: "Solid.php", Line: 4, Character: 10}, groupImplementations}, true},
		{"Square.php", 4, 30, belowType{Location{Path: "Square.php", Line: 4, Character: 12}, groupImplementations}, true},
		{"Suit.php", 4, 29, belowType{Location{Path: "Suit.php", Line: 4, Character: 5}, groupImplementations}, true},
		{"Shape.php", 4, 10, belowType{}, false},
		{"Anon.php", 4, 17, belowType{}, false},
		{"Circle.php", 5, 4, belowType{}, false},
		{"Grouped.php", 4, 13, belowType{}, false},
		{"User.php", 4, 4, belowType{}, false},
		{"User.php", 8, 25, belowType{}, false},
		{"User.php", 8, 36, belowType{}, false},
		{"User.php", 9, 59, belowType{}, false},
		{"User.php", 9, 82, belowType{}, false},
		{"User.php", 10, 45, belowType{}, false},
	} {
		got, ok := phpClauseName(Location{Path: tc.file, Line: tc.line, Character: tc.col}, phpSourceLine(tc.file))
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s:%d:%d: %+v %v, want %+v %v", tc.file, tc.line, tc.col, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPHPAlias(t *testing.T) {
	for _, tc := range []struct {
		file      string
		line, col int
		want      Location
		ok        bool
	}{
		{"Circle.php", 4, 4, Location{Path: "Circle.php", Line: 4, Character: 20}, true},
		{"Circle.php", 5, 4, Location{Path: "Circle.php", Line: 5, Character: 21}, true},
		{"Grouped.php", 4, 20, Location{Path: "Grouped.php", Line: 4, Character: 28}, true},
		{"Multi.php", 3, 8, Location{Path: "Multi.php", Line: 3, Character: 24}, true},
		{"Grouped.php", 4, 13, Location{}, false},
		{"User.php", 4, 4, Location{}, false},
		{"NsAlias.php", 7, 22, Location{}, false},
	} {
		got, ok := phpAlias(Location{Path: tc.file, Line: tc.line, Character: tc.col}, phpSourceLine(tc.file))
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s:%d:%d: %+v %v, want %+v %v", tc.file, tc.line, tc.col, got, ok, tc.want, tc.ok)
		}
	}
}
