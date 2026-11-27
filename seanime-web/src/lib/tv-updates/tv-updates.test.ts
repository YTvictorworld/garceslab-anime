import { describe, expect, it } from "vitest"
import {
    describeActionResult,
    describeBuild,
    describeInstallPhase,
    describeLastResult,
    groupChanges,
    tvUpdateErrorMessage,
} from "./tv-updates"

describe("TV updates", () => {
    it("groups changes into New, Fixes and Other in order", () => {
        const g = groupChanges([
            { type: "feat", title: "Seasons tabs", pr: 21 },
            { type: "fix", title: "Spanish subtitles", pr: 22 },
            { type: "other", title: "Faster start", pr: 23 },
            { type: "feat", title: "Search", pr: 24 },
            { type: "weird", title: "Unknown type" },
            { type: "feat" },
        ])
        expect(g.features.map(c => c.pr)).toEqual([21, 24])
        expect(g.fixes.map(c => c.pr)).toEqual([22])
        expect(g.other.map(c => c.title)).toEqual(["Faster start", "Unknown type"])
        expect(groupChanges(undefined)).toEqual({ features: [], fixes: [], other: [] })
    })

    it("describes builds without missing parts", () => {
        expect(describeBuild({ version: "1.0.1", channel: "nightly", shortCommit: "0eff776" })).toBe("v1.0.1 · nightly · 0eff776")
        expect(describeBuild({ version: "1.0.0", commit: "808b9ee0123" })).toBe("v1.0.0 · 808b9ee")
        expect(describeBuild({ version: "1.0.0", commit: null })).toBe("v1.0.0")
        expect(describeBuild(null)).toBe("Unknown")
    })

    it("describes the install phase", () => {
        expect(describeInstallPhase({ installing: false })).toBe("")
        expect(describeInstallPhase({ installing: true, installPhase: "waiting-tv" })).toBe("Waiting for the TV to turn on")
        expect(describeInstallPhase({ installing: true, installPhase: "installing" })).toBe("Installing on the TV")
    })

    it("describes the last result", () => {
        expect(describeLastResult(null)).toBe("")
        expect(describeLastResult({ ok: true, version: "1.0.1", shortCommit: "0eff776" })).toBe("Installed v1.0.1 · 0eff776")
        expect(describeLastResult({ ok: false, error: "TV is off" })).toBe("Install failed: TV is off")
    })

    it("describes the updater's answers", () => {
        expect(describeActionResult("install", true, { ok: true, queued: true })).toContain("Install requested")
        expect(describeActionResult("install", true, { ok: true, alreadyQueued: true })).toBe("The install was already requested.")
        expect(describeActionResult("install", false, { ok: false, error: "no pending build" })).toBe("The updater refused: no pending build")
        expect(describeActionResult("later", true, { ok: true })).toContain("Dismissed")
    })

    it("explains the server's errors", () => {
        expect(tvUpdateErrorMessage("PLAYBACK_ACTIVE: Ana is watching on the TV"))
            .toBe("Ana is watching on the TV. Install when the playback ends: installing closes the TV app.")
        expect(tvUpdateErrorMessage("TV_UPDATER_UNREACHABLE: the updater did not answer within 10s"))
            .toBe("The TV updater did not answer (the updater did not answer within 10s).")
        expect(tvUpdateErrorMessage("something else")).toBe("something else")
    })
})
