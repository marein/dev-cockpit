package editorintelligence

import (
	"strings"
	"testing"
)

// pythonSources are the files the Python rule was read against pyright
// 1.1.414 on: every way a base list names a class, over lines, behind a
// comment, a backslash, a module and a re-export, with type arguments and a
// metaclass, an import renaming it, and the names that are no base, in a
// call, type arguments, a keyword argument, an unpacked one, an except and a
// with clause.
var pythonSources = map[string]string{
	"base.py": `from abc import ABCMeta
from typing import Generic, TypeVar

T = TypeVar("T")


class Base:
    pass


class Box(Generic[T]):
    pass


class Meta(type):
    pass


@decorated
class Direct(Base):
    pass


class WithMeta(Base, metaclass=ABCMeta):
    pass


class Spanning(
    Box[int],  # a comment (with parens
    Base,
):
    pass


class OneLine(Base): pass


def make(cls):
    return cls


class InCall(make(Base)):
    pass


class AsTypeArg(Box[Base]):
    pass


class AsKeyword(Box[int], meta=Base):
    pass


x: Base = Base()
print(isinstance(x, Base))


def f(b: Base) -> Base:
    return b


class New[U](Base):
    pass


def outer():
    class Nested(Base):
        pass
    return Nested
`,
	"edge.py": `from pkg.base import Base, Box
from pkg.base import Base as B1, \
    Box as B2


class Joined \
        (Base):
    pass


class Quoted(Box["Base)"], Base):
    pass


class Empty():
    pass


class_ = Base


class Triple(
    Box[  # ] a closing bracket in a comment
        int
    ],
    *[Base],
    **{"x": Base},
):
    """class Fake(Base):"""


try:
    pass
except Base as err:
    pass

with Base as ctx:
    pass


class Unicodeé(Base):
    pass


class Café(Base): pass
`,
	"other.py": `import pkg.base
import pkg.base as bm
from pkg import base
from pkg.base import Base as Renamed, Box
from pkg.base import (
    Base,
)


class Dotted(pkg.base.Base):
    pass


class ModAlias(bm.Base):
    pass


class FromModule(base.Base):
    pass


class ViaRenamed(Renamed):
    pass


class ViaImport(Base, Box[str]):
    pass


class Grand(ViaImport):
    pass


y = Renamed()


class Err(Exception):
    pass


class Deco(base.Direct):
    pass


z = base.outer()
`,
	"third.py": `from pkg import Base
from pkg.base import (
    Box as Crate,
)


class FromPackage(Base):
    pass


class CrateKid(Crate[int]):
    pass


try:
    pass
except FromPackage as e:
    print(e)
`,
}

func pythonSourceLine(file string) func(n int) (string, bool) {
	rows := strings.Split(pythonSources[file], "\n")
	return func(n int) (string, bool) {
		if n < 1 || n > len(rows) {
			return "", false
		}
		return rows[n-1], true
	}
}

// The locations are the references pyright answered for Base, Box, Direct
// and the aliases, 1-based like the service maps them.
func TestPythonBaseName(t *testing.T) {
	below := func(file string, line, col int) belowType {
		return belowType{Location{Path: file, Line: line, Character: col}, groupSubclasses}
	}
	for _, tc := range []struct {
		file      string
		line, col int
		want      belowType
		ok        bool
	}{
		{"base.py", 20, 13, below("base.py", 20, 6), true},
		{"base.py", 24, 15, below("base.py", 24, 6), true},
		{"base.py", 29, 4, below("base.py", 28, 6), true},
		{"base.py", 30, 4, below("base.py", 28, 6), true},
		{"base.py", 35, 14, below("base.py", 35, 6), true},
		{"base.py", 50, 16, below("base.py", 50, 6), true},
		{"base.py", 62, 13, below("base.py", 62, 6), true},
		{"base.py", 67, 17, below("base.py", 67, 10), true},
		{"edge.py", 7, 9, below("edge.py", 6, 6), true},
		{"edge.py", 11, 13, below("edge.py", 11, 6), true},
		{"edge.py", 11, 27, below("edge.py", 11, 6), true},
		{"edge.py", 23, 4, below("edge.py", 22, 6), true},
		{"edge.py", 41, 15, below("edge.py", 41, 6), true},
		{"edge.py", 45, 11, below("edge.py", 45, 6), true},
		{"other.py", 10, 22, below("other.py", 10, 6), true},
		{"other.py", 14, 18, below("other.py", 14, 6), true},
		{"other.py", 18, 22, below("other.py", 18, 6), true},
		{"other.py", 22, 17, below("other.py", 22, 6), true},
		{"other.py", 26, 16, below("other.py", 26, 6), true},
		{"other.py", 41, 16, below("other.py", 41, 6), true},
		{"third.py", 7, 18, below("third.py", 7, 6), true},
		{"third.py", 11, 15, below("third.py", 11, 6), true},
		{"base.py", 7, 6, belowType{}, false},
		{"base.py", 42, 18, belowType{}, false},
		{"base.py", 46, 20, belowType{}, false},
		{"base.py", 50, 31, belowType{}, false},
		{"base.py", 54, 3, belowType{}, false},
		{"base.py", 54, 10, belowType{}, false},
		{"base.py", 55, 20, belowType{}, false},
		{"base.py", 58, 9, belowType{}, false},
		{"base.py", 58, 18, belowType{}, false},
		{"edge.py", 1, 21, belowType{}, false},
		{"edge.py", 11, 18, belowType{}, false},
		{"edge.py", 19, 9, belowType{}, false},
		{"edge.py", 26, 6, belowType{}, false},
		{"edge.py", 27, 12, belowType{}, false},
		{"edge.py", 34, 7, belowType{}, false},
		{"edge.py", 37, 5, belowType{}, false},
		{"other.py", 4, 21, belowType{}, false},
		{"other.py", 6, 4, belowType{}, false},
		{"other.py", 10, 13, belowType{}, false},
		{"other.py", 34, 4, belowType{}, false},
		{"third.py", 1, 16, belowType{}, false},
		{"third.py", 17, 7, belowType{}, false},
	} {
		got, ok := pythonBaseName(Location{Path: tc.file, Line: tc.line, Character: tc.col}, pythonSourceLine(tc.file))
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s:%d:%d: %+v %v, want %+v %v", tc.file, tc.line, tc.col, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPythonAlias(t *testing.T) {
	for _, tc := range []struct {
		file      string
		line, col int
		want      Location
		ok        bool
	}{
		{"other.py", 4, 21, Location{Path: "other.py", Line: 4, Character: 29}, true},
		{"third.py", 3, 4, Location{Path: "third.py", Line: 3, Character: 11}, true},
		{"edge.py", 2, 21, Location{Path: "edge.py", Line: 2, Character: 29}, true},
		{"edge.py", 3, 4, Location{Path: "edge.py", Line: 3, Character: 11}, true},
		{"other.py", 6, 4, Location{}, false},
		{"edge.py", 1, 21, Location{}, false},
		{"edge.py", 34, 7, Location{}, false},
		{"edge.py", 37, 5, Location{}, false},
		{"third.py", 1, 16, Location{}, false},
		{"third.py", 17, 7, Location{}, false},
		{"base.py", 20, 13, Location{}, false},
	} {
		got, ok := pythonAlias(Location{Path: tc.file, Line: tc.line, Character: tc.col}, pythonSourceLine(tc.file))
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s:%d:%d: %+v %v, want %+v %v", tc.file, tc.line, tc.col, got, ok, tc.want, tc.ok)
		}
	}
}

// The positions are the names of classes, a decorated and a nested one
// too, and of anything else.
func TestPythonDeclares(t *testing.T) {
	for _, tc := range []struct {
		file      string
		line, col int
		group     string
	}{
		{"base.py", 7, 6, groupSubclasses},
		{"base.py", 20, 6, groupSubclasses},
		{"base.py", 67, 10, groupSubclasses},
		{"edge.py", 6, 6, groupSubclasses},
		{"edge.py", 45, 6, groupSubclasses},
		{"base.py", 4, 0, ""},
		{"base.py", 38, 4, ""},
		{"base.py", 20, 13, ""},
		{"edge.py", 19, 0, ""},
	} {
		text, _ := pythonSourceLine(tc.file)(tc.line)
		if group := pythonDeclares(text, tc.col); group != tc.group {
			t.Errorf("%s:%d:%d: %q, want %q", tc.file, tc.line, tc.col, group, tc.group)
		}
	}
}
