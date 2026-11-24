import { getMyPlayerPrefsRequest, patchMyPlayerPrefsRequest } from "@/api/hooks/profiles.hooks"
import { serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { mediaCoreDefaultPreferences, MediaCorePreferences, mediaCorePreferencesAtom } from "@/app/(main)/_features/media-core/media-core-preferences"
import { vc_settingsRaw, VideoCoreSettings } from "@/app/(main)/_features/video-core/video-core.atoms"
import { useAtom, useAtomValue } from "jotai"
import React from "react"

const SAVE_DELAY_MS = 1500

/**
 * Garceslab Anime (phase 4): the web player's preferences follow the profile.
 * On entering a profile, its saved preferences replace this browser's; a
 * profile that has none yet takes this browser's as its first copy. Later
 * changes are saved back after a short pause. Rendered inside the per-profile
 * remount of ProfilesGate, so it starts again on every profile switch.
 */
export function ProfilePlayerPrefsSync({ profileId }: { profileId: number }) {
    const password = useAtomValue(serverAuthTokenAtom)
    const [videoCore, setVideoCore] = useAtom(vc_settingsRaw)
    const [mediaCore, setMediaCore] = useAtom(mediaCorePreferencesAtom)

    // The last values known to be on the server (JSON), null until loaded.
    const synced = React.useRef<{ videoCore: string, mediaCore: string } | null>(null)
    const latest = React.useRef({ videoCore, mediaCore })
    latest.current = { videoCore, mediaCore }

    React.useEffect(() => {
        let cancelled = false
        synced.current = null
        getMyPlayerPrefsRequest(password)
            .then(prefs => {
                if (cancelled) return
                const web = prefs?.web
                const seed: { videoCore?: unknown, mediaCore?: unknown } = {}
                let nextVideoCore = latest.current.videoCore
                let nextMediaCore = latest.current.mediaCore
                if (web?.videoCore) {
                    nextVideoCore = web.videoCore as Partial<VideoCoreSettings>
                    setVideoCore(nextVideoCore)
                } else {
                    seed.videoCore = nextVideoCore
                }
                if (web?.mediaCore) {
                    nextMediaCore = { ...mediaCoreDefaultPreferences, ...(web.mediaCore as Partial<MediaCorePreferences>), version: 2 }
                    setMediaCore(nextMediaCore)
                } else {
                    seed.mediaCore = nextMediaCore
                }
                synced.current = { videoCore: JSON.stringify(nextVideoCore), mediaCore: JSON.stringify(nextMediaCore) }
                if (seed.videoCore || seed.mediaCore) {
                    void patchMyPlayerPrefsRequest({ web: seed as any }, password).catch(() => undefined)
                }
            })
            .catch(() => {
                // Old server or offline: keep the local preferences, don't save.
            })
        return () => {
            cancelled = true
        }
        // Once per profile (the component remounts on a switch).
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [profileId])

    React.useEffect(() => {
        if (!synced.current) return
        const v = JSON.stringify(videoCore)
        const m = JSON.stringify(mediaCore)
        if (v === synced.current.videoCore && m === synced.current.mediaCore) return
        const timer = setTimeout(() => {
            const patch: { videoCore?: unknown, mediaCore?: unknown } = {}
            if (synced.current && v !== synced.current.videoCore) patch.videoCore = videoCore
            if (synced.current && m !== synced.current.mediaCore) patch.mediaCore = mediaCore
            synced.current = { videoCore: v, mediaCore: m }
            void patchMyPlayerPrefsRequest({ web: patch as any }, password).catch(() => undefined)
        }, SAVE_DELAY_MS)
        return () => clearTimeout(timer)
    }, [videoCore, mediaCore, password])

    return null
}
