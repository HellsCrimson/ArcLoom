// The centre pane's schema-form model: which block is being edited (a managed
// resource, a data source or a provider config), its meta-args and its values,
// plus the provider source + type palette the sidebar renders.
//
// This is a store rather than component state because the state must outlive
// the DOM that reads it: the form lives in the `{:else}` of the canvas toggle,
// yet it is seeded from four places while that branch is unmounted
// (restoreSnapshot, jumpToAddress, the canvas' editResource and its "add type"
// palette), so a `bind:this` would be null exactly when it matters.
//
// The async loaders stay in +page — they set busy/error and await the API; they
// call beginNew/beginEdit/beginProvider and then assign `schema` / `values`.

import type { AttrValue, NormalizedSchema, ProviderInstance, Resource, ResourceKind } from '$lib/types';

class ResourceFormStore {
	providerSource = $state('');
	resourceTypes = $state<string[]>([]);
	dataSourceTypes = $state<string[]>([]);
	selectedType = $state('');
	schema = $state<NormalizedSchema | null>(null);
	// Whether the schema form edits a managed resource or a data source.
	kind = $state<ResourceKind>('resource');

	// Center form can edit either a resource or a provider config block.
	formKind = $state<'resource' | 'provider'>('resource');
	name = $state('example');
	editingId = $state<string | null>(null);
	providerKey = $state('');
	// Alias of the provider instance being edited; editable only while creating
	// a new aliased instance (providerAliasNew).
	providerAlias = $state('');
	providerAliasNew = $state(false);
	values = $state<Record<string, AttrValue>>({});
	// count / for_each meta-args (raw HCL expressions, mutually exclusive), and
	// the edited resource's depends_on (managed on the canvas, preserved here).
	count = $state('');
	forEach = $state('');
	dependsOn = $state<string[] | undefined>(undefined);
	// provider meta-arg ("aws.euwest"); '' = the type's default provider.
	provider = $state('');

	// The provider FORM's own source/version, separate from `providerSource`
	// above — that one is the palette selector (it drives the schema fetch and
	// the sidebar's add-provider box). Sharing the two meant typing a different
	// source into the sidebar while a provider form was open, then saving,
	// silently rewrote that provider's source.
	providerFormSource = $state('');
	providerFormVersion = $state('');

	// Everything a project switch clears. Deliberately does NOT touch
	// providerSource.
	resetAll(): void {
		this.schema = null;
		this.selectedType = '';
		this.editingId = null;
		this.providerKey = '';
		this.providerAlias = '';
		this.providerAliasNew = false;
		this.providerFormSource = '';
		this.providerFormVersion = '';
		this.formKind = 'resource';
		this.values = {};
		this.name = 'example';
		this.resourceTypes = [];
		this.dataSourceTypes = [];
		this.kind = 'resource';
	}

	// resetAll plus the provider source a project switch preserves. Called once
	// when +page mounts, for the same reason as runPane.resetSession: this state
	// used to die with the component on logout.
	resetSession(): void {
		this.resetAll();
		this.providerSource = '';
	}

	// Close the form without touching the provider/type palette.
	resetForm(): void {
		this.schema = null;
		this.selectedType = '';
		this.editingId = null;
		this.providerKey = '';
		this.providerAlias = '';
		this.providerAliasNew = false;
		this.providerFormSource = '';
		this.providerFormVersion = '';
		this.formKind = 'resource';
		this.values = {};
	}

	// Switching the editing scope keeps the provider instance identity.
	resetForScope(): void {
		this.schema = null;
		this.selectedType = '';
		this.editingId = null;
		this.formKind = 'resource';
		this.values = {};
	}

	beginNew(type: string, kind: ResourceKind, name: string): void {
		this.formKind = 'resource';
		this.kind = kind;
		this.selectedType = type;
		this.editingId = null;
		this.values = {};
		this.count = '';
		this.forEach = '';
		this.dependsOn = undefined;
		this.provider = '';
		this.name = name;
	}

	beginEdit(r: Resource, kind: ResourceKind): void {
		this.formKind = 'resource';
		this.kind = kind;
		this.selectedType = r.type;
		this.editingId = r.id;
		this.name = r.name;
		this.values = JSON.parse(JSON.stringify(r.attrs ?? {}));
		this.count = r.count ?? '';
		this.forEach = r.forEach ?? '';
		this.dependsOn = r.dependsOn;
		this.provider = r.provider ?? '';
	}

	beginProvider(prov: ProviderInstance): void {
		this.formKind = 'provider';
		this.providerKey = prov.key;
		this.providerAlias = prov.alias ?? '';
		this.providerAliasNew = false;
		this.providerSource = prov.source; // palette follows the provider being edited
		this.providerFormSource = prov.source;
		this.providerFormVersion = prov.version ?? '';
		this.editingId = null;
		this.selectedType = '';
	}
}

export const form = new ResourceFormStore();
