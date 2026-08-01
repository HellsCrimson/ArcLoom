// Known configuration keys per state-backend type.
//
// Backends are part of Terraform/OpenTofu CORE, not a provider — there is no
// schema endpoint to normalize them the way internal/schema does for providers,
// so the only way to make the backend form discoverable is a hand-kept catalog.
// It is advisory, never a whitelist: the config editor stays free-form (key +
// raw HCL literal), so a key missing here can still be typed and an engine
// version that adds one is not blocked. What the catalog buys is the three
// things the generic "bucket / my-bucket" placeholder could not: required keys
// seeded when a type is picked, a key datalist + quick-add chips for the rest,
// and a per-key example so the value box stops suggesting an S3 bucket while
// the type is `http`.
//
// `example` is a raw HCL literal (quoted strings included) because that is
// exactly what the row's value box holds — Terraform forbids interpolation in a
// backend block, so these are literals, not expressions.

export type BackendField = {
	key: string;
	/** Terraform errors without it (or the backend is useless without it). */
	required?: boolean;
	/** Placeholder for the value box: a raw HCL literal. */
	example: string;
	hint: string;
};

export type BackendKind = {
	type: string;
	label: string;
	fields: BackendField[];
	/** Rendered under the type input — caveats that are not per-key. */
	note?: string;
};

export const BACKENDS: BackendKind[] = [
	{
		type: 'local',
		label: 'Local file',
		fields: [
			{ key: 'path', example: '"terraform.tfstate"', hint: 'State file path, relative to the module.' },
			{ key: 'workspace_dir', example: '"terraform.tfstate.d"', hint: 'Directory holding non-default workspace states.' }
		]
	},
	{
		type: 'http',
		label: 'HTTP (Gitea, GitLab, TFE-compatible…)',
		fields: [
			{ key: 'address', required: true, example: '"https://gitea.example.com/api/packages/OWNER/terraform/state/NAME"', hint: 'GET/POST endpoint for the state document.' },
			{ key: 'update_method', example: '"POST"', hint: 'HTTP method used to write state. Default POST.' },
			{ key: 'lock_address', example: '"https://gitea.example.com/api/packages/OWNER/terraform/state/NAME/lock"', hint: 'Lock endpoint. State locking is disabled when unset.' },
			{ key: 'lock_method', example: '"POST"', hint: 'Method for the lock call. Default LOCK.' },
			{ key: 'unlock_address', example: '"https://gitea.example.com/api/packages/OWNER/terraform/state/NAME/lock"', hint: 'Unlock endpoint. Usually the same URL as lock_address.' },
			{ key: 'unlock_method', example: '"DELETE"', hint: 'Method for the unlock call. Default UNLOCK.' },
			{ key: 'username', example: '"git"', hint: 'Basic-auth user.' },
			{ key: 'password', example: '"<token>"', hint: 'Basic-auth password or API token — use the 🔑 button to bind it to a secret.' },
			{ key: 'skip_cert_verification', example: 'true', hint: 'Skip TLS verification (self-signed endpoints).' },
			{ key: 'retry_max', example: '2', hint: 'Retries per request.' },
			{ key: 'retry_wait_min', example: '1', hint: 'Seconds before the first retry.' },
			{ key: 'retry_wait_max', example: '30', hint: 'Cap on retry backoff, in seconds.' },
			{ key: 'client_ca_certificate_pem', example: '"-----BEGIN CERTIFICATE-----\\n…"', hint: 'CA bundle for the endpoint, PEM.' },
			{ key: 'client_certificate_pem', example: '"-----BEGIN CERTIFICATE-----\\n…"', hint: 'Client certificate for mTLS, PEM.' },
			{ key: 'client_private_key_pem', example: '"-----BEGIN RSA PRIVATE KEY-----\\n…"', hint: 'Client key for mTLS, PEM.' }
		]
	},
	{
		type: 's3',
		label: 'AWS S3 (and S3-compatible: MinIO, Ceph…)',
		fields: [
			{ key: 'bucket', required: true, example: '"my-tfstate"', hint: 'Bucket holding the state.' },
			{ key: 'key', required: true, example: '"prod/terraform.tfstate"', hint: 'Object path inside the bucket.' },
			{ key: 'region', required: true, example: '"eu-west-1"', hint: 'Bucket region.' },
			{ key: 'use_lockfile', example: 'true', hint: 'S3-native locking (no DynamoDB table needed).' },
			{ key: 'dynamodb_table', example: '"terraform-locks"', hint: 'Legacy lock table.' },
			{ key: 'encrypt', example: 'true', hint: 'Server-side encryption of the state object.' },
			{ key: 'kms_key_id', example: '"arn:aws:kms:…"', hint: 'KMS key for SSE-KMS.' },
			{ key: 'profile', example: '"default"', hint: 'Shared-credentials profile.' },
			{ key: 'role_arn', example: '"arn:aws:iam::…:role/tf"', hint: 'Role assumed before touching state.' },
			{ key: 'endpoints', example: '{ s3 = "https://minio.example.com" }', hint: 'Per-service endpoint overrides (S3-compatible stores).' },
			{ key: 'use_path_style', example: 'true', hint: 'Path-style addressing, required by most S3-compatible stores.' },
			{ key: 'skip_credentials_validation', example: 'true', hint: 'Skip the STS check (non-AWS endpoints).' },
			{ key: 'skip_region_validation', example: 'true', hint: 'Allow a region name AWS does not know.' },
			{ key: 'skip_requesting_account_id', example: 'true', hint: 'Skip the account-ID lookup (non-AWS endpoints).' },
			{ key: 'workspace_key_prefix', example: '"env:"', hint: 'Prefix for non-default workspace state paths.' }
		]
	},
	{
		type: 'gcs',
		label: 'Google Cloud Storage',
		fields: [
			{ key: 'bucket', required: true, example: '"my-tfstate"', hint: 'Bucket holding the state.' },
			{ key: 'prefix', example: '"prod"', hint: 'Object prefix inside the bucket.' },
			{ key: 'credentials', example: '"/etc/arcloom/gcp.json"', hint: 'Service-account key file or JSON. GOOGLE_APPLICATION_CREDENTIALS in the server environment works too.' },
			{ key: 'impersonate_service_account', example: '"tf@project.iam.gserviceaccount.com"', hint: 'Service account to impersonate.' },
			{ key: 'encryption_key', example: '"<base64 key>"', hint: 'Customer-supplied AES-256 key.' },
			{ key: 'kms_encryption_key', example: '"projects/…/cryptoKeys/tf"', hint: 'KMS key name for CMEK.' }
		]
	},
	{
		type: 'azurerm',
		label: 'Azure Blob Storage',
		fields: [
			{ key: 'storage_account_name', required: true, example: '"tfstateacct"', hint: 'Storage account holding the container.' },
			{ key: 'container_name', required: true, example: '"tfstate"', hint: 'Blob container.' },
			{ key: 'key', required: true, example: '"prod.terraform.tfstate"', hint: 'Blob name.' },
			{ key: 'resource_group_name', example: '"rg-tfstate"', hint: 'Resource group, required for access-key/MSI lookup.' },
			{ key: 'subscription_id', example: '"00000000-0000-0000-0000-000000000000"', hint: 'Subscription.' },
			{ key: 'tenant_id', example: '"00000000-0000-0000-0000-000000000000"', hint: 'Tenant.' },
			{ key: 'client_id', example: '"00000000-0000-0000-0000-000000000000"', hint: 'Service-principal / managed-identity client id.' },
			{ key: 'client_secret', example: '"<secret>"', hint: 'Service-principal secret — use the 🔑 button to bind it to a secret.' },
			{ key: 'use_azuread_auth', example: 'true', hint: 'Authenticate to blob storage with Entra ID instead of a shared key.' },
			{ key: 'use_msi', example: 'true', hint: 'Use the host managed identity.' },
			{ key: 'sas_token', example: '"?sv=…"', hint: 'SAS token instead of a key.' },
			{ key: 'access_key', example: '"<key>"', hint: 'Storage account key — use the 🔑 button to bind it to a secret.' },
			{ key: 'snapshot', example: 'true', hint: 'Snapshot the blob before each write.' }
		]
	},
	{
		type: 'pg',
		label: 'PostgreSQL',
		fields: [
			{ key: 'conn_str', required: true, example: '"postgres://tf:pass@db.example.com/terraform?sslmode=require"', hint: 'libpq connection string; it embeds the password, so bind it to a secret with the 🔑 button.' },
			{ key: 'schema_name', example: '"terraform_remote_state"', hint: 'Schema holding the states table.' },
			{ key: 'skip_schema_creation', example: 'true', hint: 'The schema already exists; do not CREATE it.' },
			{ key: 'skip_table_creation', example: 'true', hint: 'The table already exists.' },
			{ key: 'skip_index_creation', example: 'true', hint: 'The index already exists.' }
		]
	},
	{
		type: 'kubernetes',
		label: 'Kubernetes secret',
		fields: [
			{ key: 'secret_suffix', required: true, example: '"prod"', hint: 'Suffix of the Secret name (tfstate-{workspace}-{suffix}).' },
			{ key: 'namespace', example: '"terraform"', hint: 'Namespace holding the Secret and lease.' },
			{ key: 'config_path', example: '"/etc/arcloom/kubeconfig"', hint: 'Kubeconfig file.' },
			{ key: 'config_context', example: '"homelab"', hint: 'Context inside the kubeconfig.' },
			{ key: 'in_cluster_config', example: 'true', hint: 'Use the pod service account instead of a kubeconfig.' },
			{ key: 'host', example: '"https://k8s.example.com:6443"', hint: 'API server, when not using a kubeconfig.' },
			{ key: 'token', example: '"<service account token>"', hint: 'Bearer token.' },
			{ key: 'cluster_ca_certificate', example: '"-----BEGIN CERTIFICATE-----\\n…"', hint: 'Cluster CA, PEM.' },
			{ key: 'labels', example: '{ owner = "platform" }', hint: 'Extra labels on the created Secret.' }
		]
	},
	{
		type: 'consul',
		label: 'Consul KV',
		fields: [
			{ key: 'path', required: true, example: '"terraform/prod"', hint: 'KV path holding the state.' },
			{ key: 'address', example: '"consul.example.com:8500"', hint: 'Consul agent. Defaults to CONSUL_HTTP_ADDR.' },
			{ key: 'scheme', example: '"https"', hint: 'Transport scheme.' },
			{ key: 'access_token', example: '"<token>"', hint: 'ACL token — use the 🔑 button to bind it to a secret.' },
			{ key: 'datacenter', example: '"dc1"', hint: 'Datacenter to query.' },
			{ key: 'lock', example: 'true', hint: 'Use Consul sessions to lock state.' },
			{ key: 'gzip', example: 'true', hint: 'Compress the stored state.' },
			{ key: 'ca_file', example: '"/etc/ssl/consul-ca.pem"', hint: 'CA bundle for TLS.' },
			{ key: 'cert_file', example: '"/etc/ssl/consul.pem"', hint: 'Client certificate for mTLS.' },
			{ key: 'key_file', example: '"/etc/ssl/consul-key.pem"', hint: 'Client key for mTLS.' }
		]
	},
	{
		type: 'oss',
		label: 'Alibaba Cloud OSS',
		fields: [
			{ key: 'bucket', required: true, example: '"my-tfstate"', hint: 'Bucket holding the state.' },
			{ key: 'key', example: '"terraform.tfstate"', hint: 'Object name.' },
			{ key: 'prefix', example: '"prod"', hint: 'Object prefix.' },
			{ key: 'region', example: '"cn-hangzhou"', hint: 'Bucket region.' },
			{ key: 'endpoint', example: '"oss-cn-hangzhou.aliyuncs.com"', hint: 'OSS endpoint override.' },
			{ key: 'tablestore_endpoint', example: '"https://tf.cn-hangzhou.ots.aliyuncs.com"', hint: 'TableStore endpoint used for locking.' },
			{ key: 'tablestore_table', example: '"terraform_locks"', hint: 'TableStore table used for locking.' },
			{ key: 'encrypt', example: 'true', hint: 'Server-side encryption.' }
		]
	},
	{
		type: 'cos',
		label: 'Tencent Cloud COS',
		fields: [
			{ key: 'bucket', required: true, example: '"my-tfstate-1250000000"', hint: 'Bucket holding the state.' },
			{ key: 'key', example: '"terraform.tfstate"', hint: 'Object name.' },
			{ key: 'prefix', example: '"prod"', hint: 'Object prefix.' },
			{ key: 'region', example: '"ap-guangzhou"', hint: 'Bucket region.' },
			{ key: 'encrypt', example: 'true', hint: 'Server-side encryption.' },
			{ key: 'acl', example: '"private"', hint: 'Object ACL.' }
		]
	},
	{
		type: 'remote',
		label: 'Terraform Cloud / Enterprise',
		note: 'The remote backend also needs a `workspaces { … }` block, which this editor cannot express yet (it emits attributes only). Use the http backend or a hand-edited module until then.',
		fields: [
			{ key: 'organization', required: true, example: '"my-org"', hint: 'TFC/TFE organization.' },
			{ key: 'hostname', example: '"app.terraform.io"', hint: 'TFE host.' },
			{ key: 'token', example: '"<api token>"', hint: 'API token — use the 🔑 button to bind it to a secret.' }
		]
	}
];

const byType = new Map(BACKENDS.map((b) => [b.type, b]));

export function backendKind(type: string): BackendKind | undefined {
	return byType.get(type.trim().toLowerCase());
}

export function backendFields(type: string): BackendField[] {
	return backendKind(type)?.fields ?? [];
}

export function backendField(type: string, key: string): BackendField | undefined {
	const k = key.trim();
	return backendFields(type).find((f) => f.key === k);
}
