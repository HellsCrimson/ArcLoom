import type { Project, Variable } from "$lib/types";

// Expanding a count / for_each into the instances it produces.
//
// The config holds an EXPRESSION, so this only works when that expression is
// literal enough to evaluate here: `count = 3` is three instances, and
// `for_each = toset(["a","b"])` is two named ones. Anything the engine has to
// resolve (`count = length(var.names)`, `for_each = local.teams`) is not
// knowable from the model at all — the real instance list lives in the state,
// which is why the multiplicity badge says ×N / ∀ and points at the State
// panel. A guess here would be worse than no guess, so those return null and
// the caller falls back to a single row.
//
// One indirection IS followed: `var.x` resolves against the variable's default,
// because a for_each over a variable with a literal default is the single most
// common shape ArcLoom writes.

/** How many instances to enumerate before collapsing the tail into a count. */
export const MAX_INSTANCES = 24;

export interface Expansion {
  /** Instance keys as they appear in an address: "0", "1" / "alpha", "beta". */
  keys: string[];
  /** count -> keys are indices (x[0]); for_each -> keys are strings (x["a"]). */
  kind: "count" | "for_each";
  /** Instances beyond MAX_INSTANCES that were not enumerated. */
  truncated: number;
}

/** indexSuffix renders an instance key the way the engine addresses it. */
export function indexSuffix(e: Pick<Expansion, "kind">, key: string): string {
  return e.kind === "count" ? `[${key}]` : `["${key}"]`;
}

// stripWrappers peels the set/map builders that don't change the key set, so
// `toset([...])` is evaluated as the collection inside it.
function stripWrappers(expr: string): string {
  let s = expr.trim();
  for (;;) {
    const m = /^(toset|tolist|tomap)\s*\(([\s\S]*)\)$/.exec(s);
    if (!m || !balanced(m[2])) break;
    s = m[2].trim();
  }
  return s;
}

// balanced reports whether every bracket/quote in s closes, which is what makes
// the regex peel above safe: `toset(a) + toset(b)` must not be read as one call.
function balanced(s: string): boolean {
  let depth = 0;
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote) {
      if (c === "\\") i++;
      else if (c === quote) quote = "";
      continue;
    }
    if (c === '"' || c === "'") quote = c;
    else if (c === "(" || c === "[" || c === "{") depth++;
    else if (c === ")" || c === "]" || c === "}") {
      depth--;
      if (depth < 0) return false;
    }
  }
  return depth === 0 && !quote;
}

// splitTop splits on top-level commas only, so a nested list/object stays whole.
function splitTop(s: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let quote = "";
  let start = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote) {
      if (c === "\\") i++;
      else if (c === quote) quote = "";
      continue;
    }
    if (c === '"' || c === "'") quote = c;
    else if (c === "(" || c === "[" || c === "{") depth++;
    else if (c === ")" || c === "]" || c === "}") depth--;
    else if (c === "," && depth === 0) {
      out.push(s.slice(start, i));
      start = i + 1;
    }
  }
  out.push(s.slice(start));
  return out.map((x) => x.trim()).filter((x) => x !== "");
}

/** literalString returns the value of a quoted HCL string, else null. */
function literalString(expr: string): string | null {
  const s = expr.trim();
  if (s.length < 2 || s[0] !== '"' || s[s.length - 1] !== '"') return null;
  const inner = s.slice(1, -1);
  // An interpolation isn't a literal — its value depends on something else.
  if (inner.includes("${") || inner.includes("\\")) return null;
  if (inner.includes('"')) return null;
  return inner;
}

/** varDefault returns the raw HCL of `var.<name>`'s default, if it has one. */
function varDefault(expr: string, variables: Variable[]): string | null {
  const m = /^var\.([A-Za-z_][A-Za-z0-9_-]*)$/.exec(expr.trim());
  if (!m) return null;
  const v = variables.find((x) => x.name === m[1]);
  const d = v?.default;
  if (!d) return null;
  // Only a raw expression default can be re-parsed here; a structural default
  // (entries/items) is handled by forEachKeysFromValue below.
  if (d.mode === "expression") return d.expr ?? null;
  if (d.mode === "literal" && d.entries) return null; // handled by the caller
  return null;
}

/** keysFromStructuralDefault reads keys off a variable default held as entries. */
function keysFromStructuralDefault(
  expr: string,
  variables: Variable[],
): string[] | null {
  const m = /^var\.([A-Za-z_][A-Za-z0-9_-]*)$/.exec(expr.trim());
  if (!m) return null;
  const d = variables.find((x) => x.name === m[1])?.default;
  if (!d || d.mode !== "literal") return null;
  if (d.entries) return d.entries.map((e) => e.key).filter((k) => k !== "");
  if (d.items) return d.items.map((_, i) => String(i));
  return null;
}

/**
 * expandCount enumerates a literal `count`. A non-numeric expression (or a
 * negative one) is not expandable.
 */
function expandCount(count: string): Expansion | null {
  const n = /^\d+$/.test(count.trim()) ? parseInt(count.trim(), 10) : NaN;
  if (!Number.isFinite(n)) return null;
  const shown = Math.min(n, MAX_INSTANCES);
  return {
    keys: Array.from({ length: shown }, (_, i) => String(i)),
    kind: "count",
    truncated: n - shown,
  };
}

/**
 * expandForEach enumerates a literal `for_each`: a list/set of strings, or an
 * object/map whose keys are literal. Resolves one level of `var.x` through the
 * variable's default.
 */
function expandForEach(
  forEach: string,
  variables: Variable[],
): Expansion | null {
  let expr = stripWrappers(forEach);

  const structural = keysFromStructuralDefault(expr, variables);
  if (structural) return capped(structural);

  const viaVar = varDefault(expr, variables);
  if (viaVar !== null) expr = stripWrappers(viaVar);

  if (!balanced(expr)) return null;

  // A list/set: ["alpha", "beta"] — every element must be a literal string.
  if (expr.startsWith("[") && expr.endsWith("]")) {
    const parts = splitTop(expr.slice(1, -1));
    const keys: string[] = [];
    for (const part of parts) {
      const lit = literalString(part);
      if (lit === null) return null;
      keys.push(lit);
    }
    return capped(keys);
  }

  // An object/map: { alpha = {...}, "beta" = {...} } — only the KEYS matter.
  if (expr.startsWith("{") && expr.endsWith("}")) {
    const parts = splitTop(expr.slice(1, -1).replace(/\n/g, ","));
    const keys: string[] = [];
    for (const part of parts) {
      const eq = topLevelAssign(part);
      if (eq < 0) return null;
      const rawKey = part.slice(0, eq).trim();
      const key =
        literalString(rawKey) ??
        (/^[A-Za-z_][A-Za-z0-9_-]*$/.test(rawKey) ? rawKey : null);
      if (key === null) return null;
      keys.push(key);
    }
    return capped(keys);
  }

  return null;
}

// topLevelAssign finds the `=` (or `:`) separating an object key from its value.
function topLevelAssign(s: string): number {
  let depth = 0;
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote) {
      if (c === "\\") i++;
      else if (c === quote) quote = "";
      continue;
    }
    if (c === '"' || c === "'") quote = c;
    else if (c === "(" || c === "[" || c === "{") depth++;
    else if (c === ")" || c === "]" || c === "}") depth--;
    else if (depth === 0 && (c === "=" || c === ":") && s[i + 1] !== "=")
      return i;
  }
  return -1;
}

// An EMPTY expansion is a real answer, not a failure: `count = 0` and
// `for_each = []` both mean this call creates nothing, which is worth showing.
// Only null means "the engine has to work this out".
function capped(keys: string[]): Expansion {
  const shown = keys.slice(0, MAX_INSTANCES);
  return {
    keys: shown,
    kind: "for_each",
    truncated: keys.length - shown.length,
  };
}

/**
 * expandInstances resolves a count/for_each into the instances it produces, or
 * null when the expression needs the engine. `variables` is the scope whose
 * `var.*` may be consulted — the root's for a root-scope call.
 */
export function expandInstances(
  meta: { count?: string; forEach?: string },
  variables: Variable[] = [],
): Expansion | null {
  if (meta.count) return expandCount(meta.count);
  if (meta.forEach) return expandForEach(meta.forEach, variables);
  return null;
}

/** expandCall is expandInstances for a root module call, using root variables. */
export function expandCall(
  meta: { count?: string; forEach?: string },
  project: Project | null,
): Expansion | null {
  return expandInstances(meta, project?.variables ?? []);
}
