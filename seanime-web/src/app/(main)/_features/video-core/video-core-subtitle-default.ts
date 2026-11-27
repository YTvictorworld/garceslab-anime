/**
 * Garceslab Anime: the web player's default subtitle languages, and moving
 * settings that still have the previous default to the new one, once (same
 * default and rule as the TV app, garceslab-anime-tv#11). Pure, unit tested.
 *
 * Latin American Spanish first, then any Spanish, English as the fallback.
 * Seanime's default was English only.
 */
export const VC_DEFAULT_SUBTITLE_LANGUAGES = "es-419,spa,es,spanish,eng,en,english"

/** The default Seanime shipped (and every profile using the web player was seeded with). */
export const VC_PREVIOUS_DEFAULT_SUBTITLE_LANGUAGES = "en,eng,english"

/** Saved with the video core settings once they went through the migration. */
export const VC_SUBTITLE_DEFAULT_VERSION = 2

function normalisedList(value: string) {
    return value.split(",").map(part => part.trim().toLowerCase()).filter(Boolean).join(",")
}

/**
 * Whether a saved value is the previous default. A value nobody changed can't
 * be told from one typed in by hand, so the exact previous list (ignoring case
 * and spaces) counts as never customised.
 */
export function isPreviousSubtitleDefault(value: unknown): boolean {
    return typeof value === "string" && normalisedList(value) === normalisedList(VC_PREVIOUS_DEFAULT_SUBTITLE_LANGUAGES)
}

type WithSubtitleDefault = { preferredSubtitleLanguage?: string, subtitleDefaultVersion?: number }

/**
 * The settings after the one-time migration, or null when they already went
 * through it. Only the subtitle languages change (when they are the previous
 * default); the marker is added either way, so a later hand-set English list
 * is kept.
 */
export function migrateSubtitleDefault<T extends WithSubtitleDefault>(settings: T): T | null {
    const version = settings.subtitleDefaultVersion
    if (typeof version === "number" && version >= VC_SUBTITLE_DEFAULT_VERSION) return null
    const next: T = { ...settings, subtitleDefaultVersion: VC_SUBTITLE_DEFAULT_VERSION }
    if (isPreviousSubtitleDefault(settings.preferredSubtitleLanguage)) next.preferredSubtitleLanguage = VC_DEFAULT_SUBTITLE_LANGUAGES
    return next
}
