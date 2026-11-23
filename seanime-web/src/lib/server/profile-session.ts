// Garceslab Anime: the profile session of this tab (plan §3.2, §3.9).
//
// The profile token lives in memory and in sessionStorage only (never localStorage):
// closing the tab forgets it. "Who's watching?" is shown on every app load; the token
// kept in sessionStorage only lets this tab re-enter the same profile without typing
// its PIN again after a reload.

import { useSyncExternalStore } from "react"

const STORAGE_KEY = "garceslab-profile-session"

export type ProfileSessionState = {
    token: string
    profileId: number
    /** unix seconds */
    expiresAt: number
}

// Active session of this page load (sent in X-Seanime-Profile). Starts empty on every load.
let active: ProfileSessionState | null = null
// Bumped on every change, so subscribers can reset per-profile state.
let version = 0

const listeners = new Set<() => void>()

function emit() {
    version++
    for (const l of listeners) l()
}

function readStored(): ProfileSessionState | null {
    if (typeof window === "undefined") return null
    try {
        const raw = window.sessionStorage.getItem(STORAGE_KEY)
        if (!raw) return null
        const parsed = JSON.parse(raw) as ProfileSessionState
        if (!parsed?.token || !parsed.profileId) return null
        if (parsed.expiresAt && parsed.expiresAt * 1000 <= Date.now()) return null
        return parsed
    }
    catch {
        return null
    }
}

function writeStored(s: ProfileSessionState | null) {
    if (typeof window === "undefined") return
    try {
        if (s) window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(s))
        else window.sessionStorage.removeItem(STORAGE_KEY)
    }
    catch {
    }
}

/** The token sent with every request, or "" before a profile is picked. */
export function getProfileToken(): string {
    if (active && active.expiresAt && active.expiresAt * 1000 <= Date.now()) {
        return ""
    }
    return active?.token ?? ""
}

export function getActiveProfileSession(): ProfileSessionState | null {
    return active
}

/** A session of this tab that can be resumed without the PIN (same profile only). */
export function getResumableProfileSession(profileId: number): ProfileSessionState | null {
    const s = readStored()
    return s && s.profileId === profileId ? s : null
}

export function setProfileSession(s: ProfileSessionState) {
    active = s
    writeStored(s)
    emit()
}

/** Leaves the profile in this tab (the stored token is dropped too). */
export function clearProfileSession() {
    if (!active && !readStored()) return
    active = null
    writeStored(null)
    emit()
}

export function subscribeToProfileSession(listener: () => void): () => void {
    listeners.add(listener)
    return () => {
        listeners.delete(listener)
    }
}

export function useProfileSession(): ProfileSessionState | null {
    return useSyncExternalStore(subscribeToProfileSession, () => active, () => null)
}

export function useProfileSessionVersion(): number {
    return useSyncExternalStore(subscribeToProfileSession, () => version, () => 0)
}

/** Error codes the server returns for profiles (internal/profiles/model.go, lane.go). */
export function isProfileErrorCode(err: string | undefined | null): boolean {
    if (!err) return false
    return err.startsWith("PROFILE_") || err === "PLAYBACK_BUSY" || err === "LANES_FULL"
}
