// Mirrors internal/schema/types.go and internal/api/dto.go.

export type WidgetKind =
	| 'text'
	| 'textarea'
	| 'number'
	| 'checkbox'
	| 'list'
	| 'map'
	| 'object'
	| 'block';

export interface Field {
	name: string;
	widget: WidgetKind;
	type: string;
	description?: string;
	required: boolean;
	optional: boolean;
	computed: boolean;
	sensitive: boolean;
	deprecated?: boolean;
	minItems?: number;
	maxItems?: number;
	elem?: Field;
	fields?: Field[];
}

export interface NormalizedSchema {
	provider: string;
	resourceType: string;
	version: number;
	fields: Field[];
}

export type ValueMode = 'literal' | 'reference' | 'expression' | 'secret';

// AttrValue is the wire form sent back to the server. `literal` is a raw JSON
// value whose cty type is inferred server-side.
export interface AttrValue {
	mode: ValueMode;
	literal?: unknown;
	ref?: string;
	expr?: string;
	secretRef?: string;
}

export interface Engine {
	engine: string;
	execPath: string;
	version: string;
}

export interface EnginesResponse {
	defaultEngine: string;
	engines: Engine[];
}

export interface ProviderResponse {
	provider: string;
	resourceTypes: string[];
	dataSourceTypes?: string[];
}

// ResourceKind distinguishes managed resources from data sources; both share
// the Resource shape.
export type ResourceKind = 'resource' | 'data';

export interface Resource {
	id: string;
	type: string;
	name: string;
	attrs: Record<string, AttrValue>;
	dependsOn?: string[];
	// count / for_each meta-arguments as raw HCL expressions (mutually exclusive).
	count?: string;
	forEach?: string;
}

export interface ProviderInstance {
	key: string;
	source: string;
	version?: string;
	config?: Record<string, AttrValue>;
}

export interface Variable {
	name: string;
	type?: string;
	default?: AttrValue;
	description?: string;
	sensitive?: boolean;
}

export interface Local {
	name: string;
	value: AttrValue;
}

export interface Output {
	name: string;
	value: AttrValue;
	description?: string;
	sensitive?: boolean;
}

export interface Module {
	name: string;
	source: string;
	version?: string;
	config?: Record<string, AttrValue>;
}

export interface LocalModule {
	name: string;
	variables?: Variable[];
	locals?: Local[];
	dataSources?: Resource[];
	resources: Resource[];
	outputs?: Output[];
	inputs?: Record<string, AttrValue>;
}

export interface Project {
	id: string;
	name: string;
	engine: string;
	providers: ProviderInstance[];
	variables?: Variable[];
	locals?: Local[];
	dataSources?: Resource[];
	resources: Resource[];
	modules?: Module[];
	localModules?: LocalModule[];
	outputs?: Output[];
}

// ProjectConfig is the editable subset (variables / locals / outputs).
export interface ProjectConfig {
	variables: Variable[];
	locals: Local[];
	outputs: Output[];
}

export interface SecretMeta {
	name: string;
	backend: string; // "local" | "env"
}

export interface SecretsResponse {
	localEnabled: boolean;
	secrets: SecretMeta[] | null;
}

export interface GitFileStatus {
	path: string;
	status: string; // modified | added | deleted | untracked
}

export interface GitStatus {
	initialized: boolean;
	branch: string;
	clean: boolean;
	files: GitFileStatus[] | null;
	hasCommits: boolean;
	hasRemote: boolean;
	remoteUrl?: string;
}

export interface GitCommit {
	hash: string;
	short: string;
	message: string;
	author: string;
	email: string;
	date: string;
}

export interface ProjectSummary {
	id: string;
	name: string;
	engine: string;
	resourceCount: number;
}

// A computed-only field is read-only: shown for reference, never sent as input.
export function isReadOnly(f: Field): boolean {
	return f.computed && !f.required && !f.optional;
}
