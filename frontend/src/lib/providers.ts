export interface ProviderPreset {
  id: string
  label: string
  endpoint: string
  region: string
  pathStyle: boolean
  hint?: string
}

// Endpoints with <…> parts must be completed by the user (checked on save).
export const providers: ProviderPreset[] = [
  { id: 'aws', label: 'AWS S3', endpoint: '', region: 'us-east-1', pathStyle: false, hint: 'Leave the endpoint empty for AWS.' },
  { id: 'r2', label: 'Cloudflare R2', endpoint: 'https://<account-id>.r2.cloudflarestorage.com', region: 'auto', pathStyle: true },
  { id: 'b2', label: 'Backblaze B2', endpoint: 'https://s3.<region>.backblazeb2.com', region: '<region>', pathStyle: false },
  { id: 'minio', label: 'MinIO', endpoint: 'http://localhost:9000', region: 'us-east-1', pathStyle: true },
  { id: 'wasabi', label: 'Wasabi', endpoint: 'https://s3.<region>.wasabisys.com', region: '<region>', pathStyle: false },
  { id: 'custom', label: 'Custom', endpoint: '', region: '', pathStyle: true },
]

export const providerLabel = (id: string) => providers.find((p) => p.id === id)?.label ?? id
