# Runbook: rotating the capability signing key

The capability signing key (`config.capability.signing_key_path`) signs every
capability the admin plane issues, and its Biscuit form. Replacing it outright
refuses every capability still held, since the planes no longer know the kid
those name. A rotation keeps the old key's **public** half verified until its
capabilities have expired.

## Steps

1. **Make the new key and keep the old one's public half.**

   ```bash
   openssl genpkey -algorithm ed25519 -out new.pem
   openssl pkey -in old.pem -pubout -out old.pub.pem
   ```

2. **Put both in the signing key's Secret**: `new.pem` under the key the chart
   mounts (`capabilitySigningKeySecret.key`, `key.pem` by default), and
   `old.pub.pem` under a key of its own, say `2026-10.pub.pem`.

3. **Mount the old half and list it as a previous key**, in the same change:

   ```yaml
   capabilitySigningKeySecret:
     previousKeys: [2026-10.pub.pem]
   config:
     capability:
       previous_public_keys:
         - path: /etc/paladin-capability/2026-10.pub.pem
   ```

   Set `kid` beside `path` only when the old key signed under a
   `signing_key_kid` of its own; without one, Paladin derives the kid from the
   key, as it did when the key signed.

4. **Roll out.** Each token-auth plane logs `capability: verifying, not
   signing, under a previous signing key` with the old `kid`, and
   `/.well-known/jwks.json` on the admin plane publishes both kids. New
   capabilities are signed by the new key; ones signed before still verify.

5. **Drop the old key once its capabilities have expired**: remove it from
   `previous_public_keys` and `previousKeys`, then from the Secret. Until then
   a capability it signed that is still live is refused once it is gone.

## What refuses to start

A previous key that cannot be read, is not an Ed25519 public key, names a
`kid` outside limes's namespace (`limes:` and a name), or repeats the signing
key or another previous key stops the role at boot, naming the key.
