/**
 * Garceslab Anime: TV app updates (docs/tv-updates.md). The server passes the
 * updater's GET /status through unchanged, so its shape is typed here and every
 * field is treated as optional: another self-hosted updater may send less.
 */

export type TvUpdaterChange = {
    type?: "feat" | "fix" | "other" | string
    title?: string
    pr?: number
    url?: string
}

export type TvUpdaterBuild = {
    version?: string
    commit?: string | null
    shortCommit?: string
    channel?: string
    builtAt?: string
}

export type TvUpdaterPending = TvUpdaterBuild & {
    changes?: TvUpdaterChange[]
    changesComplete?: boolean
}

export type TvUpdaterLastResult = {
    at?: string
    ok?: boolean
    version?: string
    commit?: string
    shortCommit?: string
    error?: string | null
    attempts?: number
}

export type TvUpdaterStatus = {
    channel?: string
    installed?: TvUpdaterBuild | null
    pending?: TvUpdaterPending | null
    tvOnline?: boolean
    installing?: boolean
    installPhase?: "waiting-tv" | "installing" | null | string
    lastResult?: TvUpdaterLastResult | null
}

export type GroupedChanges = {
    features: TvUpdaterChange[]
    fixes: TvUpdaterChange[]
    other: TvUpdaterChange[]
}

/** Splits the pending build's changes into New / Fixes / Other, keeping their order. */
export function groupChanges(changes: TvUpdaterChange[] | null | undefined): GroupedChanges {
    const out: GroupedChanges = { features: [], fixes: [], other: [] }
    for (const c of changes ?? []) {
        if (!c || !c.title) continue
        if (c.type === "feat") out.features.push(c)
        else if (c.type === "fix") out.fixes.push(c)
        else out.other.push(c)
    }
    return out
}

/** "1.0.1 · nightly · 0eff776", skipping what is missing. */
export function describeBuild(b: TvUpdaterBuild | null | undefined): string {
    if (!b) return "Unknown"
    const commit = b.shortCommit || (b.commit ? b.commit.slice(0, 7) : "")
    const parts = [b.version ? `v${b.version}` : "", b.channel ?? "", commit].filter(Boolean)
    return parts.length ? parts.join(" · ") : "Unknown"
}

/** A date for the user, or "" when missing or invalid. */
export function formatBuildDate(iso: string | null | undefined): string {
    if (!iso) return ""
    const d = new Date(iso)
    return isNaN(d.getTime()) ? "" : d.toLocaleString()
}

/** What the install is doing right now, or "" when nothing is happening. */
export function describeInstallPhase(s: TvUpdaterStatus): string {
    if (!s.installing) return ""
    if (s.installPhase === "waiting-tv") return "Waiting for the TV to turn on"
    return "Installing on the TV"
}

/** The last install result in one line. */
export function describeLastResult(r: TvUpdaterLastResult | null | undefined): string {
    if (!r) return ""
    const when = formatBuildDate(r.at)
    const what = describeBuild(r)
    const head = r.ok ? `Installed ${what}` : `Install failed${r.error ? `: ${r.error}` : ""}`
    return when ? `${head} (${when})` : head
}

/** The updater's answer to "Install" / "Later" (the "result" of the server's response). */
export function describeActionResult(action: "install" | "later", ok: boolean, result: Record<string, any> | null | undefined): string {
    if (!ok) {
        const err = typeof result?.error === "string" ? result.error : ""
        return err ? `The updater refused: ${err}` : "The updater refused the request"
    }
    if (action === "later") return "Dismissed. You will be told about the next version."
    if (result?.alreadyQueued) return "The install was already requested."
    return "Install requested. The TV app closes while it installs; if the TV is off, it installs when it turns on."
}

/** User-facing text for the server's errors (code before the first colon). */
export function tvUpdateErrorMessage(err: string): string {
    const code = err.split(":")[0].trim()
    const detail = err.slice(code.length + 1).trim()
    switch (code) {
        case "PLAYBACK_ACTIVE":
            return `${detail ? `${detail.replace(/\.$/, "")}. ` : ""}Install when the playback ends: installing closes the TV app.`
        case "TV_UPDATER_UNREACHABLE":
            return `The TV updater did not answer${detail ? ` (${detail})` : ""}.`
        case "TV_UPDATER_NOT_CONFIGURED":
            return "No TV updater is configured."
        case "TV_UPDATER_SET_BY_ENV":
            return "This value is set by an environment variable on the server."
        case "TV_UPDATER_INVALID_SETTINGS":
            return detail || "Invalid TV updater settings."
    }
    return err
}
