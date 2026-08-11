export function supported() {
  return Boolean(window.PublicKeyCredential && navigator.credentials);
}

export function toBytes(value) {
  const padded = String(value || "").replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  return Uint8Array.from(raw, (ch) => ch.charCodeAt(0));
}

export function toBase64URL(buffer) {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  for (let i = 0; i < bytes.length; i += 1) binary += String.fromCharCode(bytes[i]);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function withBytes(list) {
  return (list || []).map((entry) => ({ ...entry, id: toBytes(entry.id) }));
}

export function creationOptions(options) {
  return {
    ...options,
    challenge: toBytes(options.challenge),
    user: { ...options.user, id: toBytes(options.user.id) },
    excludeCredentials: withBytes(options.excludeCredentials),
  };
}

export function requestOptions(options) {
  return {
    ...options,
    challenge: toBytes(options.challenge),
    allowCredentials: withBytes(options.allowCredentials),
  };
}

export function attestationPayload(credential) {
  return {
    id: credential.id,
    rawId: toBase64URL(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment || undefined,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: toBase64URL(credential.response.clientDataJSON),
      attestationObject: toBase64URL(credential.response.attestationObject),
      transports: credential.response.getTransports ? credential.response.getTransports() : [],
    },
  };
}

export function assertionPayload(credential) {
  return {
    id: credential.id,
    rawId: toBase64URL(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment || undefined,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: toBase64URL(credential.response.clientDataJSON),
      authenticatorData: toBase64URL(credential.response.authenticatorData),
      signature: toBase64URL(credential.response.signature),
      userHandle: credential.response.userHandle ? toBase64URL(credential.response.userHandle) : null,
    },
  };
}

export function deviceLabel() {
  const ua = navigator.userAgent;
  const isIPad = /iPad/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
  if (/iPhone/.test(ua)) return "iPhone";
  if (isIPad) return "iPad";
  if (/Android/.test(ua)) return "Android";
  if (/Mac/.test(ua)) return "Mac";
  if (/Windows/.test(ua)) return "Windows";
  if (/Linux/.test(ua)) return "Linux";
  return "Passkey";
}

export function ceremonyError(error) {
  if (!error) return "The passkey could not be used.";
  if (error.name === "NotAllowedError") return "The passkey prompt was cancelled or timed out.";
  if (error.name === "InvalidStateError") return "This authenticator already holds a passkey for this cockpit.";
  if (error.name === "SecurityError") return "This address cannot carry a passkey. It needs a domain name over https.";
  return error.message || "The passkey could not be used.";
}
