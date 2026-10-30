package editorintelligence

import (
	"fmt"
	"strings"
	"testing"
)

func TestTSDeclares(t *testing.T) {
	for _, tc := range []struct {
		line      string
		character int
		want      string
	}{
		{"export interface Shape {", 17, groupImplementations},
		{"export interface Shape {", 22, groupImplementations},
		{"export interface SubjectLike<In, Out = In> extends Subscribable<Out>, Observer<In> {", 20, groupImplementations},
		{"interface Ünïcode {}", 12, groupImplementations},
		{"export abstract class Base implements Shape {", 23, groupSubclasses},
		{"class BehaviorSubject<T> extends PerSubscriptionSubjectBase<T> {", 6, groupSubclasses},
		{"export default class Foo {}", 21, groupSubclasses},
		{"export const Lit = class Named extends Painted {};", 26, groupSubclasses},
		{"export abstract class Base implements Shape {", 40, ""},
		{"export class Square implements Shape {", 8, ""},
		{"export class Square implements Shape {", 12, ""},
		{"let holder: Shape;", 13, ""},
		{"export type ShapeLike = Shape & {};", 14, ""},
		{"export function total(shapes: Shape[]) {", 17, ""},
		{"const subclass = Foo;", 18, ""},
	} {
		if got := tsDeclares(tc.line, tc.character); got != tc.want {
			t.Errorf("%q at %d: %q, want %q", tc.line, tc.character, got, tc.want)
		}
	}
}

// The locations are the ones tsgo 7.0.0-dev answered the implementation
// request with, on test files and on rxjs, which shows every shape: a class
// or an interface by its name, a class expression and an anonymous default
// class by their start, a value typed with the type, a type alias naming it
// and a variable holding a class by something else.
func TestTSHeritage(t *testing.T) {
	for _, tc := range []struct {
		lines     string
		character int
		ok        bool
		last      int
	}{
		{"export interface Solid extends Shape {", 17, true, 1},
		{"export abstract class Base implements Shape {", 22, true, 1},
		{"class BehaviorSubject<T> extends PerSubscriptionSubjectBase<T> {", 6, true, 1},
		{"export abstract class PerSubscriptionSubjectBase<In, Out = In> extends ColdObservable<Out> implements SubjectLike<In, Out> {", 22, true, 1},
		{"export class Tricky extends Base<{ a: number }> implements Shape { area() { return 1; } }", 13, true, 1},
		{"export class Wide\n  extends Base\n  implements\n    Shape,\n    Iterable<number> {\n  area() { return 1; }\n}", 13, true, 5},
		{"export class Arrow<T extends () => void = () => void> // a { in a comment\n  extends Base<'{'> /* and { here */ {", 13, true, 2},
		{"export class Lost\n  extends Base", 13, true, 1},
		{"export const Anon = class implements Shape {", 20, true, 1},
		{"export const Lit = class extends Painted {};", 19, true, 1},
		{"export default class implements Shape {", 0, true, 1},
		{"export const unit: Shape = { area: () => 1 };", 27, false, 0},
		{"export const made = { area(): number { return 2; } } satisfies Shape;", 20, false, 0},
		{"  return { area: () => 4 };", 9, false, 0},
		{"export const double: Fn = (x) => x * 2;", 26, false, 0},
		{"export class Holder { shape: Shape = { area: () => 0 }; }", 37, false, 0},
		{"type SubjectLike<T> = import('./util/types.js').SubjectLike<T>;", 48, false, 0},
		{"let AsyncSubject: AsyncSubjectConstructor;", 4, false, 0},
		{"const connectors: Subject<string>[] = [];", 42, false, 0},
		{`/** @typedef {import("./shape").Shape} ShapeT */`, 32, false, 0},
	} {
		rows := strings.Split(tc.lines, "\n")
		line := func(n int) (string, bool) {
			if n < 1 || n > len(rows) {
				return "", false
			}
			return rows[n-1], true
		}
		head, ok := tsHeritage(Location{Line: 1, Character: tc.character}, line)
		if ok != tc.ok || head.last != tc.last {
			t.Errorf("%q at %d: %d %v, want %d %v", rows[0], tc.character, head.last, ok, tc.last, tc.ok)
		}
	}
}

// The names a head holds outside its brackets are where the types it
// extends or implements stand, by the line and the start of each word; a
// type argument, a constraint, a comment and the body hold none.
func TestTSHeritageNames(t *testing.T) {
	for _, tc := range []struct {
		lines     string
		character int
		want      string
	}{
		{"export interface Solid extends Shape {", 17, "1:Solid 1:extends 1:Shape"},
		{"export class Qualified implements all.Shape {", 13, "1:Qualified 1:implements 1:all 1:Shape"},
		{"export class Generic<T extends Form> extends Square {", 13, "1:Generic 1:extends 1:Square"},
		{"export class Wrapped extends Square implements Iterable<Form> {", 13, "1:Wrapped 1:extends 1:Square 1:implements 1:Iterable"},
		{"export class Tricky extends Base<{ a: number }> implements Shape { area(): Shape { return this; } }", 13, "1:Tricky 1:extends 1:Base 1:implements 1:Shape"},
		{"export class Wide\n  extends Base\n  implements\n    Shape,\n    Iterable<number> {\n  shape: Shape;\n}", 13, "1:Wide 2:extends 2:Base 3:implements 4:Shape 5:Iterable"},
		{"export class Arrow<T extends () => void = () => void> // a Shape in a comment\n  extends Base<'{'> /* and Shape here */ {", 13, "1:Arrow 2:extends 2:Base"},
		{"export const Anon = class implements Ünïcode {", 20, "1:implements 1:Ünïcode"},
		{"export class Lost\n  extends Base", 13, "1:Lost"},
	} {
		rows := strings.Split(tc.lines, "\n")
		line := func(n int) (string, bool) {
			if n < 1 || n > len(rows) {
				return "", false
			}
			return rows[n-1], true
		}
		head, ok := tsHeritage(Location{Path: "a.ts", Line: 1, Character: tc.character}, line)
		var got []string
		for _, name := range head.names {
			text := rows[name.Line-1]
			start := UTF16Offset(text, name.Character)
			end := start + len(text[start:]) - len(strings.TrimLeftFunc(text[start:], isIdentifierRune))
			if name.Path != "a.ts" {
				t.Errorf("%q: name without its path %+v", rows[0], name)
			}
			got = append(got, fmt.Sprintf("%d:%s", name.Line, text[start:end]))
		}
		if !ok || strings.Join(got, " ") != tc.want {
			t.Errorf("%q: %q %v, want %q", rows[0], strings.Join(got, " "), ok, tc.want)
		}
	}
}
