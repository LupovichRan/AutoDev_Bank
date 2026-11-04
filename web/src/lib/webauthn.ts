// Passkey ceremonies. Prefers the native JSON helpers
// (PublicKeyCredential.parse*OptionsFromJSON / credential.toJSON) and falls
// back to manual base64url conversion on older browsers.
import { apiPost } from './api';
import { t } from './i18n.svelte';

export function b64urlEncode(buf: ArrayBuffer | ArrayBufferView): string {
  const bytes = buf instanceof ArrayBuffer ? new Uint8Array(buf) : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function b64urlDecode(s: string): ArrayBuffer {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (s.length % 4)) % 4);
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

type Json = Record<string, unknown>;
interface DescriptorJSON {
  id: string;
  type: string;
  transports?: string[];
}

/** go-webauthn wraps options as {publicKey: {...}}; accept both shapes. */
export function unwrapOptions(options: Json): Json {
  const pk = options['publicKey'];
  return pk && typeof pk === 'object' ? (pk as Json) : options;
}

function descriptors(list: unknown): PublicKeyCredentialDescriptor[] | undefined {
  if (!Array.isArray(list)) return undefined;
  return (list as DescriptorJSON[]).map((d) => ({
    ...d,
    id: b64urlDecode(d.id),
    type: 'public-key' as const,
    transports: d.transports as AuthenticatorTransport[] | undefined,
  }));
}

export function creationOptionsFromJSON(json: Json): PublicKeyCredentialCreationOptions {
  const o = unwrapOptions(json);
  const user = o['user'] as Json;
  return {
    ...(o as unknown as PublicKeyCredentialCreationOptions),
    challenge: b64urlDecode(o['challenge'] as string),
    user: { ...(user as unknown as PublicKeyCredentialUserEntity), id: b64urlDecode(user['id'] as string) },
    excludeCredentials: descriptors(o['excludeCredentials']),
  };
}

export function requestOptionsFromJSON(json: Json): PublicKeyCredentialRequestOptions {
  const o = unwrapOptions(json);
  return {
    ...(o as unknown as PublicKeyCredentialRequestOptions),
    challenge: b64urlDecode(o['challenge'] as string),
    allowCredentials: descriptors(o['allowCredentials']),
  };
}

interface NativeJSONStatics {
  parseCreationOptionsFromJSON?: (o: Json) => PublicKeyCredentialCreationOptions;
  parseRequestOptionsFromJSON?: (o: Json) => PublicKeyCredentialRequestOptions;
}

function statics(): NativeJSONStatics {
  return (globalThis as unknown as { PublicKeyCredential?: NativeJSONStatics }).PublicKeyCredential ?? {};
}

/** Serialise a credential the way PublicKeyCredential.toJSON() does. */
export function credentialToJSON(cred: PublicKeyCredential): Json {
  const native = (cred as unknown as { toJSON?: () => Json }).toJSON;
  if (typeof native === 'function') return native.call(cred);
  const r = cred.response;
  const response: Json = { clientDataJSON: b64urlEncode(r.clientDataJSON) };
  if ('attestationObject' in r) {
    const a = r as AuthenticatorAttestationResponse;
    response['attestationObject'] = b64urlEncode(a.attestationObject);
    if (typeof a.getTransports === 'function') response['transports'] = a.getTransports();
  } else {
    const a = r as AuthenticatorAssertionResponse;
    response['authenticatorData'] = b64urlEncode(a.authenticatorData);
    response['signature'] = b64urlEncode(a.signature);
    if (a.userHandle) response['userHandle'] = b64urlEncode(a.userHandle);
  }
  return {
    id: cred.id,
    rawId: b64urlEncode(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response,
  };
}

export function webauthnSupported(): boolean {
  return typeof navigator !== 'undefined' && !!navigator.credentials && typeof globalThis.PublicKeyCredential !== 'undefined';
}

interface Begin {
  challenge_id: string;
  options: Json;
}

async function create(options: Json): Promise<Json> {
  const s = statics();
  const publicKey = s.parseCreationOptionsFromJSON
    ? s.parseCreationOptionsFromJSON(unwrapOptions(options))
    : creationOptionsFromJSON(options);
  const cred = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error(t('webauthn.noneCreated'));
  return credentialToJSON(cred);
}

async function get(options: Json): Promise<Json> {
  const s = statics();
  const publicKey = s.parseRequestOptionsFromJSON
    ? s.parseRequestOptionsFromJSON(unwrapOptions(options))
    : requestOptionsFromJSON(options);
  const cred = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error(t('webauthn.noneSelected'));
  return credentialToJSON(cred);
}

export async function register(handle: string, displayName: string): Promise<{ principal_id: string }> {
  const begin = await apiPost<Begin>('/api/auth/register/begin', { handle, display_name: displayName });
  const credential = await create(begin.options);
  return apiPost('/api/auth/register/finish', { challenge_id: begin.challenge_id, credential });
}

export async function login(): Promise<{ principal_id: string }> {
  const begin = await apiPost<Begin>('/api/auth/login/begin', {});
  const credential = await get(begin.options);
  return apiPost('/api/auth/login/finish', { challenge_id: begin.challenge_id, credential });
}

/** Fresh assertion bound to the transfer payload hash (dynamic linking). */
export async function stepUp(payloadHash: string): Promise<{ stepped_up: boolean }> {
  const begin = await apiPost<Begin>('/api/auth/stepup/begin', { payload_hash: payloadHash });
  const credential = await get(begin.options);
  return apiPost('/api/auth/stepup/finish', { challenge_id: begin.challenge_id, credential });
}

/** Turn a DOMException from navigator.credentials into a sentence for people. */
export function describeWebAuthnError(e: unknown): string {
  if (e instanceof DOMException) {
    if (e.name === 'NotAllowedError') return t('webauthn.cancelled');
    if (e.name === 'InvalidStateError') return t('webauthn.exists');
    if (e.name === 'SecurityError') return t('webauthn.insecure');
  }
  return e instanceof Error ? e.message : String(e);
}
