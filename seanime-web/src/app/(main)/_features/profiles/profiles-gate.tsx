import { Status } from "@/api/generated/types"
import { CreateProfileScreen } from "@/app/(main)/_features/profiles/create-profile-screen"
import { useProfileSwitcher } from "@/app/(main)/_features/profiles/use-profile-switcher"
import { WhosWatching } from "@/app/(main)/_features/profiles/whos-watching"
import { clearProfileSession, useProfileSession } from "@/lib/server/profile-session"
import React from "react"

type ProfilesGateProps = {
    status: Status
    children?: React.ReactNode
}

/**
 * Nothing of the app renders without a profile (plan §3.2, §3.9): first run → "Create your
 * profile", otherwise → "Who's watching?". Servers without status.profiles skip the gate.
 */
export function ProfilesGate({ status, children }: ProfilesGateProps) {
    const session = useProfileSession()
    const { fetchStatus, applyStatus } = useProfileSwitcher()
    const enabled = !!status.profiles?.enabled
    const currentId = status.currentProfile?.id

    // A /status that doesn't match the tab's profile (token expired, PIN changed, profile
    // deleted, or a stale response): check again with the current token, then drop it.
    const sessionToken = session?.token
    const sessionProfileId = session?.profileId
    React.useEffect(() => {
        if (!enabled || !sessionToken || currentId === sessionProfileId) return
        let cancelled = false
        fetchStatus(sessionToken)
            .then(st => {
                if (cancelled) return
                if (st?.currentProfile?.id === sessionProfileId) applyStatus(st)
                else clearProfileSession()
            })
            .catch(() => {
                // Network errors keep the session; PROFILE_* 401s already cleared it.
            })
        return () => {
            cancelled = true
        }
    }, [enabled, sessionToken, sessionProfileId, currentId, fetchStatus, applyStatus])

    if (!enabled) return children

    if (status.profiles?.needsSetup && !session) {
        return <CreateProfileScreen firstRun />
    }

    if (!session) {
        return <WhosWatching />
    }

    // Remount everything when the profile changes so no per-profile state survives.
    return <React.Fragment key={session.profileId}>{children}</React.Fragment>
}
