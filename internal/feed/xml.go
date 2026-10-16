package feed

import "strings"

// A minimal XML writer, a byte-for-byte port of lib/xml.ts. Kept instead of
// encoding/xml so the output matches the Deno version exactly (feed bodies
// and ETags must not change for subscribers).

type attr struct{ key, value string }

type tag struct {
	name     string
	text     string // used when children is nil
	children []tag
	attrs    []attr
}

var (
	textEscaper = escaper("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = escaper("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
)

// escaper also drops the characters XML 1.0 doesn't allow (control
// characters other than tab and newlines, U+FFFE, U+FFFF): one of them in
// an NRK text would make the whole feed unreadable for strict players.
// Valid text is unchanged, so feed bodies and ETags stay the same.
func escaper(pairs ...string) *strings.Replacer {
	for c := rune(0); c < 0x20; c++ {
		if c != '\t' && c != '\n' && c != '\r' {
			pairs = append(pairs, string(c), "")
		}
	}
	pairs = append(pairs, "\uFFFE", "", "\uFFFF", "")
	return strings.NewReplacer(pairs...)
}

func text(name, value string, attrs ...attr) tag { return tag{name: name, text: value, attrs: attrs} }

func node(name string, children []tag, attrs ...attr) tag {
	if children == nil {
		children = []tag{}
	}
	return tag{name: name, children: children, attrs: attrs}
}

func writeAttrs(b *strings.Builder, attrs []attr) {
	for _, a := range attrs {
		b.WriteString(" ")
		b.WriteString(a.key)
		b.WriteString(`="`)
		attrEscaper.WriteString(b, a.value)
		b.WriteString(`"`)
	}
}

// writeTag streams straight into b. A tag is self-closing when its content
// is empty: text tags with empty text, and nodes without children (a child
// tag never renders empty).
func writeTag(b *strings.Builder, t tag) {
	b.WriteString("<")
	b.WriteString(t.name)
	writeAttrs(b, t.attrs)
	if (t.children == nil && t.text == "") || (t.children != nil && len(t.children) == 0) {
		b.WriteString("/>")
		return
	}
	b.WriteString(">")
	if t.children == nil {
		textEscaper.WriteString(b, t.text)
	} else {
		for _, child := range t.children {
			writeTag(b, child)
		}
	}
	b.WriteString("</")
	b.WriteString(t.name)
	b.WriteString(">")
}

// serialize writes the XML declaration, an optional stylesheet processing
// instruction and the root element.
func serialize(declaration []attr, stylesheet []attr, root tag, sizeHint int) string {
	var b strings.Builder
	b.Grow(sizeHint)
	b.WriteString("<?xml")
	writeAttrs(&b, declaration)
	b.WriteString("?>")
	if stylesheet != nil {
		b.WriteString("<?xml-stylesheet")
		writeAttrs(&b, stylesheet)
		b.WriteString("?>")
	}
	writeTag(&b, root)
	return b.String()
}
