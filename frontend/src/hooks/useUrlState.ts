"use client";

import { useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";

import { withParams, type ParamUpdates } from "@/lib/table/urlState";

/** How a value reads from the query string and the parameters it writes. */
export interface UrlCodec<T> {
  read(params: URLSearchParams): T;
  write(value: T): ParamUpdates;
}

/** A text parameter; empty is its absence. Built once, at module level. */
export function textParam(key: string): UrlCodec<string> {
  return {
    read: (params) => params.get(key) ?? "",
    write: (value) => ({ [key]: value || null }),
  };
}

/** The value a flag parameter carries when it is on. */
const FLAG_ON = "true";

/** An on/off parameter, present only when on. Built once, at module level. */
export function flagParam(key: string): UrlCodec<boolean> {
  return {
    read: (params) => params.get(key) === FLAG_ON,
    write: (on) => ({ [key]: on ? FLAG_ON : null }),
  };
}

/** The address bar's parameters now, which a replace changes at once. */
function currentParams(): URLSearchParams {
  return typeof window === "undefined"
    ? new URLSearchParams()
    : new URLSearchParams(window.location.search);
}

/** Two values are the same view when they write the same parameters. */
function sameView<T>(codec: UrlCodec<T>, a: T, b: T): boolean {
  return JSON.stringify(codec.write(a)) === JSON.stringify(codec.write(b));
}

/**
 * Replaces the URL's parameters without a navigation or a history entry, so
 * typing in a search does not fill the back button. Next.js syncs
 * useSearchParams with the native call.
 */
function replaceParams(updates: ParamUpdates): void {
  const { pathname, search, hash } = window.location;
  const next = withParams(search, updates);
  if (next === search) return;
  window.history.replaceState(null, "", `${pathname}${next}${hash}`);
}

/**
 * State mirrored into the query string. It starts from the URL, writes every
 * change back, and takes up a navigation that changes its parameters, such as
 * a link to the same list with another search. Without a codec it is plain
 * state starting at `initial`.
 *
 * The state is held locally and the URL follows it, rather than the other way
 * round: the router answers a replace in a transition, and a search box whose
 * value waited for it would lose its caret on every keystroke.
 *
 * A codec that keeps its identity between renders spares the effect that
 * writes the URL from running again for nothing.
 */
export function useUrlState<T>(
  codec: UrlCodec<T> | null,
  initial: T,
): [T, React.Dispatch<React.SetStateAction<T>>] {
  // Read only to learn that the URL changed; the address bar is the truth,
  // since the router may still be catching up with a replace made here.
  const signal = useSearchParams()?.toString() ?? "";
  const [value, setValue] = useState<T>(() =>
    codec ? codec.read(currentParams()) : initial,
  );
  const [seen, setSeen] = useState(signal);
  if (codec && seen !== signal) {
    setSeen(signal);
    const fromUrl = codec.read(currentParams());
    if (!sameView(codec, value, fromUrl)) setValue(fromUrl);
  }
  useEffect(() => {
    if (codec) replaceParams(codec.write(value));
  }, [codec, value]);
  return [value, setValue];
}
