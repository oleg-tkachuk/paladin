// Package bucketstate names the values of buckets.provision_state that more
// than one package must spell alike: the admin API, which reports them, and
// the object state machine, which moves bytes only out of a bucket that still
// exists. A leaf with no imports, so the state machine links nothing of the
// admin API's.
package bucketstate

// Ready is a bucket that exists on its backend and is not being deleted.
const Ready = "ready"
