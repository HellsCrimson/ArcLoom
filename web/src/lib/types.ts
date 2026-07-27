// Mirrors internal/schema/types.go and internal/api/dto.go.

export type WidgetKind =
	| 'text'
	| 'textarea'
	| 'number'
	| 'checkbox'
	| 'list'
	| 'map'
	| 'object'
	| 'block'
	/** A provider-declared dynamic value: no child schema, edited free-form. */
	| 'dynamic';

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
// value whose cty type is inferred server-side. A collection literal whose
// elements are themselves mode-switchable (so a reference/secret can nest
// inside) is carried structurally via `items` (list/set/block) or `entries`
// (map/object) instead of `literal`.
export interface AttrValue {
	mode: ValueMode;
	literal?: unknown;
	ref?: string;
	expr?: string;
	secretRef?: string;
	items?: AttrValue[];
	entries?: AttrEntry[];
}

export interface AttrEntry {
	key: string;
	value: AttrValue;
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
	// provider meta-argument selecting an aliased instance ("aws.euwest");
	// empty/absent = the type's default provider.
	provider?: string;
}

export interface ProviderInstance {
	key: string;
	// Alias distinguishes extra instances of the same provider (multi-region);
	// empty for the default instance.
	alias?: string;
	source: string;
	version?: string;
	config?: Record<string, AttrValue>;
}

// ProviderUpdate reports whether a newer version of a provider source is
// available (best-effort registry check; keyed by source).
export interface ProviderUpdate {
	key: string;
	source: string;
	pinned?: string; // the version constraint on the instance
	current?: string; // installed (lock) or exact pin
	latest?: string; // highest stable available
	update: boolean;
	error?: string; // registry unreachable, etc.
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
	// count / for_each meta-args on the module call (raw HCL, mutually exclusive).
	count?: string;
	forEach?: string;
}

export interface LocalModule {
	name: string;
	variables?: Variable[];
	locals?: Local[];
	dataSources?: Resource[];
	resources: Resource[];
	outputs?: Output[];
	inputs?: Record<string, AttrValue>;
	// count / for_each on the root's call to this module (not on its body).
	count?: string;
	forEach?: string;
}

// Backend configures remote state storage (terraform { backend "type" {...} }).
export interface Backend {
	type: string;
	config?: Record<string, AttrValue>;
}

/**
 * A companion file emitted next to the generated HCL — a template, script or
 * JSON document referenced with file()/templatefile(). `path` is relative to
 * the root module, so "templates/x.tftpl" is `${path.module}/templates/x.tftpl`.
 */
export interface ProjectFile {
	path: string;
	content: string;
}

export interface Project {
	id: string;
	name: string;
	engine: string;
	/** terraform { required_version = … } — empty omits the attribute. */
	requiredVersion?: string;
	backend?: Backend;
	providers: ProviderInstance[];
	variables?: Variable[];
	locals?: Local[];
	dataSources?: Resource[];
	resources: Resource[];
	modules?: Module[];
	localModules?: LocalModule[];
	outputs?: Output[];
	files?: ProjectFile[];
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
	/** Whether secrets can be created here: the local encrypted store or an
	 *  external one (env secrets are read-only). */
	writable: boolean;
	/** Which store is writable: "local", "vault", or "" when none is. */
	backend: string;
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
	ahead: number;
	behind: number;
}

export interface GitBranch {
	name: string;
	current: boolean;
	short: string;
}

export interface GitCommit {
	hash: string;
	short: string;
	message: string;
	author: string;
	email: string;
	date: string;
}

// AuditEntry is one recorded action in the admin-visible audit log.
export interface AuditEntry {
	id: string;
	time: string;
	user: string;
	action: string;
	projectId?: string;
	detail?: string;
}

// ACLEntry grants one user a set of project-scoped permissions.
export interface ACLEntry {
	userId: string;
	username: string;
	perms: string[];
}

// ProjectACL is a project's access list. `restricted` false means the project
// is open (global RBAC applies).
export interface ProjectACL {
	restricted: boolean;
	entries: ACLEntry[];
}

// GitCredMeta describes a project's stored remote credential without exposing
// any secret value.
export interface GitCredMeta {
	stored: boolean;
	authType: string; // "https" | "ssh"
	username?: string;
	hasToken: boolean;
	hasSSHKey: boolean;
	hasPassphrase: boolean;
}

// DriftConfig is a project's scheduled drift-check setting (periodic
// refresh-only plans on the server).
export interface DriftConfig {
	enabled: boolean;
	intervalMinutes: number;
}

// DriftStatus is the outcome of the latest scheduled drift check. In-memory on
// the server: absent until the first check after a restart.
export interface DriftStatus {
	checkedAt: string;
	drifted: boolean;
	add: number;
	change: number;
	destroy: number;
	replace: number;
	/** The drifted resource addresses (capped server-side), so a count is inspectable. */
	addresses?: string[];
	/** How many of them the refresh also saw changed outside ArcLoom (the rest are unapplied config edits). */
	outOfBand?: number;
	error?: string;
}

export interface ProjectSummary {
	id: string;
	name: string;
	engine: string;
	resourceCount: number;
	driftEnabled?: boolean;
	drift?: DriftStatus;
}

// A computed-only field is read-only: shown for reference, never sent as input.
export function isReadOnly(f: Field): boolean {
	return f.computed && !f.required && !f.optional;
}
