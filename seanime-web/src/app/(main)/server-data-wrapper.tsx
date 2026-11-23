import { API_ENDPOINTS } from "@/api/generated/endpoints"
import { Status } from "@/api/generated/types"
import { useGetStatus } from "@/api/hooks/status.hooks"
import { serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { GettingStartedPage } from "@/app/(main)/_features/getting-started/getting-started-page"
import { ProfilesGate } from "@/app/(main)/_features/profiles/profiles-gate"
import { useServerStatus, useSetServerStatus } from "@/app/(main)/_hooks/use-server-status"
import { LoadingOverlayWithLogo } from "@/components/shared/loading-overlay-with-logo"
import { LuffyError } from "@/components/shared/luffy-error"
import { logger } from "@/lib/helpers/debug"
import { usePathname } from "@/lib/navigation"
import { WSEvents } from "@/lib/server/ws-events"
import { useQueryClient } from "@tanstack/react-query"
import { useAtomValue } from "jotai"
import React from "react"
import { useWebsocketMessageListener } from "./_hooks/handle-websockets"

type ServerDataWrapperProps = {
    host: string
    children?: React.ReactNode
}

export function ServerDataWrapper(props: ServerDataWrapperProps) {

    const {
        host,
        children,
        ...rest
    } = props

    const pathname = usePathname()
    const serverStatus = useServerStatus()
    const setServerStatus = useSetServerStatus()
    const queryClient = useQueryClient()
    const password = useAtomValue(serverAuthTokenAtom)
    const { data: queryServerStatus, isLoading, refetch } = useGetStatus()
    const resolvedServerStatus = serverStatus ?? queryServerStatus

    React.useEffect(() => {
        if (queryServerStatus) {
            logger("SERVER").info("Server status", queryServerStatus)
            setServerStatus(queryServerStatus)
        }
    }, [queryServerStatus, setServerStatus])

    useWebsocketMessageListener({
        type: WSEvents.ANILIST_DATA_LOADED,
        onMessage: () => {
            logger("Data Wrapper").info("Anilist data loaded, refetching server status")
            refetch()
        },
    })

    useWebsocketMessageListener({
        type: WSEvents.SETTINGS_CHANGED,
        onMessage: () => {
            // The event carries no data; /status returns the effective settings of this tab's profile.
            logger("Data Wrapper").info("Settings changed, refetching server status")
            void queryClient.invalidateQueries({ queryKey: [API_ENDPOINTS.SETTINGS.GetSettings.key] })
            refetch()
        },
    })

    const authenticated = !resolvedServerStatus?.serverHasPassword || !!password || pathname === "/public/auth"

    React.useEffect(() => {
        if (resolvedServerStatus?.serverHasPassword && !password && pathname !== "/public/auth") {
            window.location.href = "/public/auth"
            console.warn("Redirecting to auth")
        }
    }, [resolvedServerStatus?.serverHasPassword, password, pathname])

    // Refetch the server status every 2 seconds if serverReady is false
    // This is a fallback to the websocket
    const intervalId = React.useRef<number | null>(null)
    React.useEffect(() => {
        if (!resolvedServerStatus?.serverReady) {
            intervalId.current = window.setInterval(() => {
                logger("Data Wrapper").info("Refetching server status")
                refetch()
            }, 2000)
        }
        return () => {
            logger("Data Wrapper").info("Clearing interval")
            if (intervalId.current) {
                window.clearInterval(intervalId.current)
                intervalId.current = null
            }
        }
    }, [resolvedServerStatus?.serverReady, refetch])

    /**
     * If the server status is loading or doesn't exist, show the loading overlay
     */
    if (isLoading || !resolvedServerStatus || !authenticated) return <LoadingOverlayWithLogo />
    if (!resolvedServerStatus.serverReady) return <LoadingOverlayWithLogo title="L o a d i n g" />

    const currentServerStatus = resolvedServerStatus

    /**
     * If the app is updating, show a different screen
     */
    if (currentServerStatus.updating) {
        return <div className="container max-w-3xl py-10">
            <div className="mb-4 flex justify-center w-full">
                <img src="/garceslab-logo.png" alt="logo" className="w-14 h-auto" />
            </div>
            <p className="text-center text-lg">
                Seanime is currently updating. Refresh the page once the update is complete and the connection has been reestablished.
            </p>
        </div>
    }

    // Garceslab Anime: "Who's watching?" / "Create your profile" before anything else.
    // AniList login is gone (profiles never use an AniList account).
    return (
        <ProfilesGate status={currentServerStatus}>
            <ServerDataContent status={currentServerStatus} pathname={pathname}>
                {children}
            </ServerDataContent>
        </ProfilesGate>
    )
}

function ServerDataContent({ status, pathname, children }: { status: Status, pathname: string, children?: React.ReactNode }) {
    /**
     * If the server status doesn't have settings, show the getting started page
     */
    if (!status.settings) {
        return <GettingStartedPage status={status} />
    }

    /**
     * Check feature flag routes
     */
    if (!status.mediastreamSettings?.transcodeEnabled && pathname.startsWith("/mediastream")) {
        return <LuffyError title="Transcoding not enabled" />
    }

    return children
}
