// A signal that the console's session is gone, raised by the transport when
// the session role answers Unauthenticated, and heard by AuthContext, which
// signs the page out. An event rather than an import, so the transport needs
// nothing from React.

export const SESSION_ENDED_EVENT = "paladin:session-ended";

// The page's one bus for it: the transport and AuthContext share this module.
const bus = new EventTarget();

export function signalSessionEnded(target: EventTarget = bus): void {
  target.dispatchEvent(new Event(SESSION_ENDED_EVENT));
}

export function onSessionEnded(
  listener: () => void,
  target: EventTarget = bus,
): () => void {
  target.addEventListener(SESSION_ENDED_EVENT, listener);
  return () => target.removeEventListener(SESSION_ENDED_EVENT, listener);
}
