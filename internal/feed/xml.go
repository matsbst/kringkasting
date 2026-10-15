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
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
)

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

func serialize(declaration []attr, root tag, sizeHint int) string {
	var b strings.Builder
	b.Grow(sizeHint)
	b.WriteString("<?xml")
	writeAttrs(&b, declaration)
	b.WriteString("?>")
	writeTag(&b, root)
	return b.String()
}
