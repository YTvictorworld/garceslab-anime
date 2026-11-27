import { describe, expect, it } from "vitest"
import { isTrackLanguageMatch } from "../../../../lib/helpers/language"
import {
    isPreviousSubtitleDefault,
    migrateSubtitleDefault,
    VC_DEFAULT_SUBTITLE_LANGUAGES,
    VC_SUBTITLE_DEFAULT_VERSION,
} from "./video-core-subtitle-default"

type Track = { number: number, language?: string, label?: string, default?: boolean }

/** The preference loop of getDefaultSubtitleTrackNumber (video-core-subtitles.ts), which needs the browser to import. */
function firstPreferred(pref: string, tracks: Track[]) {
    for (const lang of pref.split(",").map(l => l.trim()).filter(Boolean)) {
        const found = tracks.find(t => isTrackLanguageMatch(t, lang))
        if (found) return found.number
    }
    return null
}

describe("web player subtitle default (Spanish first)", () => {
    it("the default is Latin American Spanish, any Spanish, then English", () => {
        // vc_initialSettings (video-core.atoms.ts) uses these.
        expect(VC_DEFAULT_SUBTITLE_LANGUAGES).toBe("es-419,spa,es,spanish,eng,en,english")
        expect(VC_SUBTITLE_DEFAULT_VERSION).toBe(2)
    })

    it("with the default, a MultiSub release (English first and default) starts in es-419", () => {
        const tracks = [
            { number: 1, language: "en", label: "English", default: true },
            { number: 2, language: "es-419", label: "Spanish (Latin America)" },
            { number: 3, language: "es-ES", label: "Spanish (Spain)" },
            { number: 4, language: "pt-BR", label: "Portuguese (Brazil)" },
        ]
        expect(firstPreferred(VC_DEFAULT_SUBTITLE_LANGUAGES, tracks)).toBe(2)
        // Legacy tags only: any Spanish.
        expect(firstPreferred(VC_DEFAULT_SUBTITLE_LANGUAGES, [
            { number: 1, language: "eng", label: "English", default: true },
            { number: 2, language: "spa", label: "Español" },
        ])).toBe(2)
        // No Spanish: English.
        expect(firstPreferred(VC_DEFAULT_SUBTITLE_LANGUAGES, [
            { number: 1, language: "fre", label: "French" },
            { number: 2, language: "eng", label: "English" },
        ])).toBe(2)
    })

    it("recognises the previous default whatever its case and spacing, nothing else", () => {
        for (const v of ["en,eng,english", " EN , eng,English ", "en, eng, english,"]) expect(isPreviousSubtitleDefault(v)).toBe(true)
        for (const v of ["en,eng", "jpn", "eng,en,english", "spa,es", "", undefined, 1]) expect(isPreviousSubtitleDefault(v)).toBe(false)
    })

    it("migrates the previous default once and touches nothing else", () => {
        const old = { preferredSubtitleLanguage: "en,eng,english", preferredAudioLanguage: "jpn", subtitleDelay: 0.5 }
        const migrated = migrateSubtitleDefault(old)!
        expect(migrated).toEqual({
            preferredSubtitleLanguage: VC_DEFAULT_SUBTITLE_LANGUAGES,
            preferredAudioLanguage: "jpn",
            subtitleDelay: 0.5,
            subtitleDefaultVersion: VC_SUBTITLE_DEFAULT_VERSION,
        })
        // Idempotent, and English chosen again by hand afterwards is kept.
        expect(migrateSubtitleDefault(migrated)).toBeNull()
        expect(migrateSubtitleDefault({ ...migrated, preferredSubtitleLanguage: "en,eng,english" })).toBeNull()
    })

    it("leaves customised languages alone (only the marker is added)", () => {
        for (const value of ["en,eng", "jpn", "spa,es"]) {
            const migrated = migrateSubtitleDefault({ preferredSubtitleLanguage: value })!
            expect(migrated.preferredSubtitleLanguage).toBe(value)
            expect(migrated.subtitleDefaultVersion).toBe(VC_SUBTITLE_DEFAULT_VERSION)
        }
    })
})
