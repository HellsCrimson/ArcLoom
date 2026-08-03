// Rendering an AttrValue as HCL text, and reading a literal collection back.
//
// The meta-args (`count`, `for_each`) are RAW HCL STRINGS on the model — the
// engine has to evaluate them, so there is nothing else they could be. That
// left the UI with a single text box: authoring the map a `for_each` iterates
// meant typing HCL by hand, brace by brace, with no idea whether it parsed.
//
// These two functions let a structural editor drive that box: `toHclText`
// serializes what the editor produced, and `parseHclCollection` reads an
// existing expression back into a structure so re-opening the form does not
// force the user back into raw text.
//
// The parser is deliberately partial, the same rule expand.ts follows: it
// understands literal collections (objects, lists, strings, numbers, bools,
// nested), keeps anything else as an opaque **expression leaf** (so
// `{ a = var.x }` still round-trips), and returns null when the text is not a
// collection at all — the caller then leaves the raw box alone rather than
// mangling an expression it does not understand.

import type { AttrValue } from "$lib/types";
import {
  balanced,
  hasComment,
  literalString,
  splitTop,
  stripComments,
  topLevelAssign,
} from "$lib/model/expand";

/** toHclText renders a value as an HCL expression. */
export function toHclText(v: AttrValue | undefined, indent = 0): string {
  if (!v) return "";
  if (v.mode === "reference") return v.ref ?? "";
  if (v.mode === "expression") return v.expr ?? "";
  if (v.mode === "secret") {
    // Mirrors hclgen: a secret is referenced as its generated input variable.
    return v.secretRef ? `var.${v.secretRef}` : "";
  }
  const pad = "  ".repeat(indent + 1);
  const close = "  ".repeat(indent);
  if (v.entries) {
    if (v.entries.length === 0) return "{}";
    const rows = v.entries
      .filter((e) => e.key.trim() !== "")
      .map((e) => `${pad}${quoteKey(e.key.trim())} = ${toHclText(e.value, indent + 1)}`);
    return rows.length ? `{\n${rows.join("\n")}\n${close}}` : "{}";
  }
  if (v.items) {
    if (v.items.length === 0) return "[]";
    const rows = v.items.map((it) => `${pad}${toHclText(it, indent + 1)}`);
    return `[\n${rows.join(",\n")}\n${close}]`;
  }
  const lit = v.literal;
  if (lit === undefined || lit === null) return '""';
  if (typeof lit === "string") return JSON.stringify(lit);
  if (typeof lit === "number" || typeof lit === "boolean") return String(lit);
  return JSON.stringify(lit);
}

// An HCL object key is bare when it is an identifier, quoted otherwise — the
// same choice hclgen makes, and what keeps `{ alpha = … }` readable.
function quoteKey(key: string): string {
  return /^[A-Za-z_][A-Za-z0-9_-]*$/.test(key) ? key : JSON.stringify(key);
}

/**
 * parseHclCollection reads an HCL object/list literal into a structural value,
 * for an EDITING round trip: the result goes into a structural editor and comes
 * back out through toHclText.
 *
 * Returns null when the text is not a collection (a reference, a function call,
 * a comprehension) — those stay raw text — and, for the same reason, when it
 * carries a COMMENT. The structural form has nowhere to store one, so parsing
 * `web = "10.0.1.0/24" # frontend` into rows would delete that note the next
 * time the form saved. Refusing keeps the raw box, which keeps the comment.
 *
 * Use readHclCollection instead when nothing is written back.
 */
export function parseHclCollection(text: string): AttrValue | null {
  if (hasComment(text ?? "")) return null;
  return readHclCollection(text);
}

/**
 * readHclCollection is parseHclCollection for READ-ONLY analysis (member
 * previews, shape detection): it strips comments and reads what is left, so a
 * commented collection still explains itself in the for_each panel even though
 * the Build editor refuses to open on it.
 */
export function readHclCollection(text: string): AttrValue | null {
  const s = stripComments(text ?? "").trim();
  if (!s || !balanced(s)) return null;
  if (!(isObject(s) || isList(s))) return null;
  const parsed = parseValue(s);
  // Brace-shaped but not a collection — `{ for k, v in … }` is a comprehension,
  // and parseValue keeps it as an opaque leaf. Structurally it is a scalar, so
  // handing it to the collection editor would show a one-line text box claiming
  // to be a map. Null instead: the caller keeps the raw expression box.
  if (parsed.entries === undefined && parsed.items === undefined) return null;
  return parsed;
}

function isObject(s: string): boolean {
  return s.startsWith("{") && s.endsWith("}");
}
function isList(s: string): boolean {
  return s.startsWith("[") && s.endsWith("]");
}

// parseValue never fails: anything it cannot read structurally becomes an
// expression leaf, which toHclText re-emits verbatim. That is what makes the
// round trip safe for a collection with a reference or a function call inside.
function parseValue(text: string): AttrValue {
  const s = text.trim();

  if (balanced(s) && isObject(s)) {
    const inner = s.slice(1, -1);
    const parts = splitTop(inner.replace(/\r?\n/g, ","));
    const entries: { key: string; value: AttrValue }[] = [];
    for (const part of parts) {
      const eq = topLevelAssign(part);
      if (eq < 0) return expr(s); // not `key = value` — keep the whole thing raw
      const rawKey = part.slice(0, eq).trim();
      const key = literalString(rawKey) ?? (isIdent(rawKey) ? rawKey : null);
      if (key === null) return expr(s);
      entries.push({ key, value: parseValue(part.slice(eq + 1)) });
    }
    return { mode: "literal", entries };
  }

  if (balanced(s) && isList(s)) {
    const parts = splitTop(s.slice(1, -1).replace(/\r?\n/g, ","));
    return { mode: "literal", items: parts.map(parseValue) };
  }

  const str = literalString(s);
  if (str !== null) return { mode: "literal", literal: str };
  if (s === "true" || s === "false") return { mode: "literal", literal: s === "true" };
  if (/^-?\d+(\.\d+)?$/.test(s)) return { mode: "literal", literal: Number(s) };
  return expr(s);
}

function expr(text: string): AttrValue {
  return { mode: "expression", expr: text.trim() };
}

function isIdent(s: string): boolean {
  return /^[A-Za-z_][A-Za-z0-9_-]*$/.test(s);
}

/**
 * renderHclAttrs writes a block body as `key = value` lines, sorted like hclgen
 * writes them so the text matches the generated config.
 */
export function renderHclAttrs(attrs: Record<string, AttrValue>): string {
  return Object.keys(attrs)
    .sort()
    .map((k) => `${k} = ${toHclText(attrs[k])}`)
    .join("\n");
}

/**
 * parseHclAttrs reads `key = value` lines back into attributes — the escape
 * hatch for a block holding something the form cannot show: an attribute the
 * provider's schema does not declare has no widget, so it is invisible AND
 * preserved on every save, with no way to delete it.
 *
 * Returns null on the first line it cannot read, so a typo leaves the stored
 * config untouched rather than half-applied.
 *
 * Comments are stripped rather than refused, unlike parseHclCollection: this
 * box is rendered FROM the model by renderHclAttrs, which never emits a
 * comment, so there is no comment here to protect — one the user types is
 * transient either way. Stripping is what makes a trailing comment behave: it
 * used to end up inside the value (`tags = {} # note` stored the note as part
 * of the expression), while a comment on its own line was already dropped.
 */
export function parseHclAttrs(text: string): Record<string, AttrValue> | null {
  const out: Record<string, AttrValue> = {};
  // Split on newlines that are not inside a nested collection, so a multi-line
  // object value stays with its key.
  const lines = splitTopLines(stripComments(text));
  for (const line of lines) {
    const trimmed = line.trim();
    // A comment-only line is now whitespace, so the empty check covers it.
    if (!trimmed) continue;
    const eq = topLevelAssign(trimmed);
    if (eq < 0) return null;
    const key = trimmed.slice(0, eq).trim().replace(/^"(.*)"$/, "$1");
    const rest = trimmed.slice(eq + 1).trim();
    if (!key || !rest || !balanced(rest)) return null;
    out[key] = parseValue(rest);
  }
  return out;
}

// splitTopLines breaks on newlines at bracket depth 0 only.
function splitTopLines(text: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let quote = "";
  let start = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quote) {
      if (c === "\\") i++;
      else if (c === quote) quote = "";
      continue;
    }
    if (c === '"' || c === "'") quote = c;
    else if (c === "(" || c === "[" || c === "{") depth++;
    else if (c === ")" || c === "]" || c === "}") depth--;
    else if (c === "\n" && depth === 0) {
      out.push(text.slice(start, i));
      start = i + 1;
    }
  }
  out.push(text.slice(start));
  return out;
}

/**
 * eachReferences lists the addresses a block's fields can use to reach the
 * current instance, for the reference picker: `each.key` / `each.value` (plus
 * one entry per known member attribute) under a for_each, `count.index` under a
 * count. They are ordinary traversals, which is exactly what Reference mode
 * emits — the answer to "which mode do I use for each.value.x".
 */
export function eachReferences(
  meta: { count?: string; forEach?: string },
  valueFields: string[] = [],
): string[] {
  if (meta.count?.trim()) return ["count.index"];
  if (!meta.forEach?.trim()) return [];
  return ["each.key", "each.value", ...valueFields.map((f) => `each.value.${f}`)];
}
