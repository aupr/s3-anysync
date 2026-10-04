import { toAgentError } from './api'

const messages: Record<string, string> = {
  busy: 'Another s3-anysync process is using the data folder. Try again in a moment.',
  locked: 'Another machine is syncing this action. Try again in a minute.',
  'not-found': 'It no longer exists; it may have been removed elsewhere.',
  'conflict-pending': 'This action has conflicts. Resolve them first.',
  'conflict-edit': 'The action was changed elsewhere while you edited it.',
  'passphrase-missing': 'This action is encrypted. Enter its passphrase.',
  decrypt: 'The passphrase is wrong or the cloud data is damaged.',
  'keycheck-missing': "The action's key check file is missing in the bucket, so the passphrase cannot be verified.",
  'root-missing': "An item's folder is missing (unmounted drive or renamed folder?).",
  'access-denied': 'The bucket refused access. Check the access key, the secret and the permissions.',
  'no-such-bucket': 'The bucket does not exist. Check the profile.',
  'lock-lost': 'This machine lost the cloud lock during the run. Nothing is damaged; run it again.',
  unsupported: 'This is not supported on this system.',
  unavailable: 'The agent is shutting down. Try again in a moment.',
  cancelled: 'Cancelled.',
  unreachable: 'The agent is not running.',
  unauthorized: 'The agent restarted. Reconnecting…',
}

// Codes whose agent message belongs next to the form field.
export const fieldCodes = new Set(['invalid', 'protected-path'])

export interface Described {
  code: string
  message: string
  details?: string
  field: boolean
}

export function describeError(e: unknown): Described {
  const err = toAgentError(e)
  if (fieldCodes.has(err.code)) return { code: err.code, message: err.message, field: true }
  if (err.code === 'in-use') return { code: err.code, message: err.message, field: false }
  const known = messages[err.code]
  if (known) return { code: err.code, message: known, details: err.message, field: false }
  return { code: err.code, message: 'Something went wrong.', details: err.message, field: false }
}
