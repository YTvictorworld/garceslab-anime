import { buildSeaQuery } from "@/api/client/requests"
import { API_ENDPOINTS } from "@/api/generated/endpoints"
import { ProfileSession, Status } from "@/api/generated/types"
import { profileLogoutRequest } from "@/api/hooks/profiles.hooks"
import { serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { useSetServerStatus } from "@/app/(main)/_hooks/use-server-status"
import { clearProfileSession, getResumableProfileSession, setProfileSession } from "@/lib/server/profile-session"
import { useQueryClient } from "@tanstack/react-query"
import { useAtomValue } from "jotai"
import React from "react"

/**
 * Enter / leave a profile in this tab. Switching drops every cached query (plan §3.9.5);
 * the websocket reconnects by itself when the session changes.
 */
export function useProfileSwitcher() {
    const qc = useQueryClient()
    const password = useAtomValue(serverAuthTokenAtom)
    const setServerStatus = useSetServerStatus()

    // "" = explicitly no profile header (the cookie may still apply, the UI ignores it).
    const fetchStatus = React.useCallback((profileToken: string) => buildSeaQuery<Status>({
        endpoint: API_ENDPOINTS.STATUS.GetStatus.endpoint,
        method: "GET",
        password,
        profileToken,
    }), [password])

    const applyStatus = React.useCallback((st: Status | undefined) => {
        if (!st) return
        qc.setQueryData([API_ENDPOINTS.STATUS.GetStatus.key], st)
        setServerStatus(st)
    }, [qc, setServerStatus])

    /** Activates a session returned by /profiles/:id/select. Throws if the token doesn't work. */
    const enter = React.useCallback(async (session: Pick<ProfileSession, "profileToken" | "expiresAt"> & { profileId: number }) => {
        const st = await fetchStatus(session.profileToken)
        if (!st?.currentProfile || st.currentProfile.id !== session.profileId) {
            throw new Error("PROFILE_INVALID_TOKEN")
        }
        qc.clear()
        applyStatus(st)
        setProfileSession({ token: session.profileToken, profileId: session.profileId, expiresAt: session.expiresAt })
    }, [fetchStatus, qc, applyStatus])

    /** Re-enters this tab's previous session of the profile without the PIN. */
    const resume = React.useCallback(async (profileId: number): Promise<boolean> => {
        const stored = getResumableProfileSession(profileId)
        if (!stored) return false
        try {
            await enter({ profileToken: stored.token, expiresAt: stored.expiresAt, profileId })
            return true
        }
        catch {
            return false
        }
    }, [enter])

    /** Back to "Who's watching?". */
    const leave = React.useCallback(async () => {
        clearProfileSession()
        qc.clear()
        try {
            await profileLogoutRequest(password)
        }
        catch {
        }
        try {
            applyStatus(await fetchStatus(""))
        }
        catch {
        }
    }, [qc, password, fetchStatus, applyStatus])

    return { enter, resume, leave, fetchStatus, applyStatus }
}
