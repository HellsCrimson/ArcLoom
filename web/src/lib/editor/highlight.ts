// Lightweight HCL tokenizer for the editor's live main.tf panel. Colors match
// the ArcLoom Editor design.
export interface Tok {
	t: string;
	c: string;
}

const COL = {
	text: '#c9d3dc',
	comment: '#6b7480',
	keyword: '#7aa2f7',
	typ: '#7ee787',
	ref: '#ffa657',
	str: '#79c0ff',
	punct: '#8b949e'
};

const KEYWORDS = new Set([
	'terraform',
	'resource',
	'provider',
	'variable',
	'output',
	'module',
	'data',
	'locals',
	'required_providers'
]);

const TOKEN_RE =
	/(\s+)|("(?:[^"\\]|\\.)*")|(\d+(?:\.\d+)?)|([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_-]*)+)|([A-Za-z_][A-Za-z0-9_]*)|([{}[\]()=,])|(.)/g;

export function highlightHcl(src: string): Tok[][] {
	return src.replace(/\n$/, '').split('\n').map(highlightLine);
}

function highlightLine(line: string): Tok[] {
	const trimmed = line.trimStart();
	if (trimmed.startsWith('#') || trimmed.startsWith('//')) {
		return [{ t: line || ' ', c: COL.comment }];
	}
	const isHeader = /^(resource|data|provider|module)\b/.test(trimmed);
	const toks: Tok[] = [];
	let m: RegExpExecArray | null;
	TOKEN_RE.lastIndex = 0;
	while ((m = TOKEN_RE.exec(line))) {
		if (m[1]) toks.push({ t: m[1], c: COL.text });
		else if (m[2]) toks.push({ t: m[2], c: isHeader ? COL.typ : COL.str });
		else if (m[3]) toks.push({ t: m[3], c: COL.str });
		else if (m[4]) toks.push({ t: m[4], c: COL.ref });
		else if (m[5]) {
			const w = m[5];
			if (KEYWORDS.has(w)) toks.push({ t: w, c: COL.keyword });
			else if (w === 'true' || w === 'false' || w === 'null') toks.push({ t: w, c: COL.str });
			else toks.push({ t: w, c: COL.text });
		} else if (m[6]) toks.push({ t: m[6], c: COL.punct });
		else toks.push({ t: m[7], c: COL.text });
	}
	return toks.length ? toks : [{ t: ' ', c: COL.text }];
}
