/**
 * Minimal XML serializer, replacing the previous dependency on
 * https://github.com/olaven/serialize-xml with a local implementation.
 */

export type Attribute = [string, string];

export type Tag = {
  name: string;
  children: Tag[] | string;
  attributes: Attribute[];
};

export type Declaration = {
  attributes: Attribute[];
};

function escapeText(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function escapeAttribute(value: string): string {
  return escapeText(value)
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

export function tag(name: string, children: Tag[] | string = "", attributes: Attribute[] = []): Tag {
  return { name, children, attributes };
}

export function declaration(attributes: Attribute[]): Declaration {
  return { attributes };
}

function serializeAttributes(attributes: Attribute[]): string {
  return attributes
    .map(([key, value]) => ` ${key}="${escapeAttribute(value)}"`)
    .join("");
}

function serializeTag(t: Tag): string {
  const attributes = serializeAttributes(t.attributes);
  const content = typeof t.children === "string" ? escapeText(t.children) : t.children.map(serializeTag).join("");

  if (content === "") {
    return `<${t.name}${attributes}/>`;
  }
  return `<${t.name}${attributes}>${content}</${t.name}>`;
}

export function serialize(decl: Declaration, root: Tag): string {
  return `<?xml${serializeAttributes(decl.attributes)}?>${serializeTag(root)}`;
}
