// Describing the collection a `for_each` iterates.
//
// `expand.ts` answers "which instances does this produce" (the keys). This
// module answers the question the form actually raises: WHAT IS IN THE SET.
// Writing `each.value.name` means knowing what a member looks like, and that
// definition lives somewhere else entirely — a variable default or a local, one
// modal away — so the expression `var.clusters` told you nothing about the
// thing you were iterating.
//
// Everything here is derived from the model and is therefore best-effort by the
// same rule expand.ts follows: if the engine has to resolve it, say so rather
// than guess.

import type { AttrValue, Field, Local, Variable } from "$lib/types";
import { elemField } from "$lib/form/defaults";
import { parseHclType } from "$lib/model/hcltype";
import {
  expandInstances,
  refParts,
  resolveRef,
  type Expansion,
  type Scope,
} from "$lib/model/expand";

export interface ForEachElement {
  key: string;
  /** One-line rendering of the member, when the model holds its value. */
  preview?: string;
}

export interface ForEachInfo {
  /** The `var.x` / `local.x` the collection comes from, when it is one. */
  ref: { kind: "variable" | "local"; name: string } | null;
  /**
   * The ref was reached THROUGH a comprehension (`{ for k, v in var.x : … }`),
   * so its members describe the shape but not the final key set — the filter or
   * transform is the engine's. Worth showing anyway: it is where `each.value`'s
   * attributes come from, and it is the definition the user wants to open.
   */
  derived: boolean;
  /** The ref resolves to a declaration in this scope. */
  found: boolean;
  /** Declared HCL type, when the source is a typed variable. */
  typeText?: string;
  expansion: Expansion | null;
  elements: ForEachElement[];
  /** Attribute names available on `each.value`, when members are objects. */
  valueFields: string[];
  /** A set of strings: `each.value` IS `each.key`, there are no fields. */
  valueIsKey: boolean;
}

const MAX_PREVIEW_ENTRIES = 4;
const MAX_PREVIEW_LEN = 72;

/** previewAttr renders a value compactly enough for one line of a table. */
export function previewAttr(v: AttrValue | undefined): string {
  if (!v) return "";
  if (v.mode === "reference") return v.ref ?? "";
  if (v.mode === "expression") return v.expr ?? "";
  if (v.mode === "secret") return `🔒 ${v.secretRef ?? ""}`;
  if (v.entries) {
    const shown = v.entries.slice(0, MAX_PREVIEW_ENTRIES);
    const body = shown
      .map((e) => `${e.key} = ${previewAttr(e.value)}`)
      .join(", ");
    const more = v.entries.length - shown.length;
    return clip(`{ ${body}${more > 0 ? `, +${more}` : ""} }`);
  }
  if (v.items) {
    const shown = v.items.slice(0, MAX_PREVIEW_ENTRIES);
    const body = shown.map((it) => previewAttr(it)).join(", ");
    const more = v.items.length - shown.length;
    return clip(`[${body}${more > 0 ? `, +${more}` : ""}]`);
  }
  const lit = v.literal;
  if (lit === undefined || lit === null) return "";
  return clip(typeof lit === "string" ? JSON.stringify(lit) : String(lit));
}

function clip(s: string): string {
  return s.length > MAX_PREVIEW_LEN ? s.slice(0, MAX_PREVIEW_LEN - 1) + "…" : s;
}

/** fieldsOf lists the attribute names of an object-ish member field. */
function fieldsOf(f: Field | null): string[] {
  if (!f) return [];
  const elem = elemField(f);
  return (elem.fields ?? []).map((c) => c.name);
}

/**
 * describeForEach resolves a for_each expression into what the UI can show
 * about the collection: where it is declared, which keys it produces, what a
 * member looks like, and what `each.value` therefore offers.
 */
export function describeForEach(
  forEach: string,
  scope: Scope,
): ForEachInfo | null {
  const expr = (forEach ?? "").trim();
  if (!expr) return null;

  const bare = stripSetWrapper(expr);
  // A comprehension names its source collection: `{ for k, v in var.x : … }`.
  // Falling back to it is what keeps the panel useful for the filtered case,
  // which is otherwise just "ask the engine".
  const comprehension = /\bfor\s+[A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)?\s+in\s+((?:var|local)\.[A-Za-z_][\w-]*)/.exec(bare);
  const srcExpr = refParts(bare) ? bare : (comprehension?.[1] ?? bare);
  const derived = !refParts(bare) && comprehension !== null;
  const ref = refParts(srcExpr);
  const value = resolveRef(srcExpr, scope);
  const declared = ref
    ? ref.kind === "variable"
      ? (scope.variables ?? []).find((v) => v.name === ref.name)
      : (scope.locals ?? []).find((l) => l.name === ref.name)
    : undefined;

  const expansion = expandInstances({ forEach: expr }, scope);
  const typeText =
    ref?.kind === "variable" ? (declared as Variable | undefined)?.type : undefined;

  // Members: the structural value is the rich case (it is what the Variables
  // modal edits and what hclimport lifts an all-literal collection into).
  const members = memberValues(value);
  const elements: ForEachElement[] = (expansion?.keys ?? []).map((key, i) => ({
    key,
    preview: members ? previewAttr(members[key] ?? members[String(i)]) : undefined,
  }));

  // What `each.value` offers: the declared type first (it is the contract, and
  // it holds even when no default exists), else the shape of the first member.
  const typed = typeText ? parseHclType(typeText) : null;
  let valueFields = fieldsOf(typed);
  const firstMember = members ? Object.values(members)[0] : undefined;
  if (valueFields.length === 0 && firstMember?.entries) {
    valueFields = firstMember.entries.map((e) => e.key).filter(Boolean);
  }
  // `toset(["a","b"])`: each.value IS the string. Claimed only with evidence —
  // either the members are in hand and none is an object, or the declared type
  // says the element is a scalar. A raw-HCL collection whose members we cannot
  // see says nothing, and guessing "value == key" there would be a lie about
  // the very thing this panel exists to answer.
  // An inline `[...]` that expanded at all is evidence too: expand.ts only
  // returns keys for a list whose every element is a literal string.
  const inlineList =
    expansion !== null && stripSetWrapper(expr).startsWith("[");
  const valueIsKey =
    valueFields.length === 0 &&
    (inlineList ||
      (members !== null
        ? Object.values(members).every((m) => !m?.entries)
        : typed !== null && !(elemField(typed).fields?.length ?? 0)));

  return {
    ref,
    derived,
    found: declared !== undefined,
    typeText,
    expansion,
    elements,
    valueFields,
    valueIsKey,
  };
}

// stripSetWrapper peels one toset()/tomap()/tolist() so `toset(var.x)` still
// resolves to the variable behind it — the same collection, same keys.
function stripSetWrapper(expr: string): string {
  const m = /^(toset|tolist|tomap)\s*\(([\s\S]*)\)$/.exec(expr.trim());
  return m ? m[2].trim() : expr.trim();
}

/**
 * memberValues maps each member of a structural collection to its value, keyed
 * the way the engine addresses it. Null when the model holds no structure (a
 * raw HCL expression), where only the keys are knowable.
 */
function memberValues(
  v: AttrValue | undefined,
): Record<string, AttrValue> | null {
  if (!v || v.mode !== "literal") return null;
  if (v.entries) {
    const out: Record<string, AttrValue> = {};
    for (const e of v.entries) if (e.key) out[e.key] = e.value;
    return out;
  }
  if (v.items) {
    const out: Record<string, AttrValue> = {};
    // A for_each over a list of strings is keyed BY the string, not the index —
    // `toset(["a","b"])` produces x["a"], so key by the literal when there is
    // one and fall back to the index for anything else.
    v.items.forEach((it, i) => {
      const key =
        it.mode === "literal" && typeof it.literal === "string"
          ? it.literal
          : String(i);
      out[key] = it;
    });
    return out;
  }
  return null;
}

/** scopeOf is the variables+locals a scope's expressions can refer to. */
export function scopeOf(
  s: { variables?: Variable[]; locals?: Local[] } | null | undefined,
): Scope {
  return { variables: s?.variables ?? [], locals: s?.locals ?? [] };
}
