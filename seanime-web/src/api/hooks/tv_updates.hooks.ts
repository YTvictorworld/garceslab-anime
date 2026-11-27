import { getSeaErrorCode, useServerMutation, useServerQuery } from "@/api/client/requests"
import { SaveTvUpdatesSettings_Variables } from "@/api/generated/endpoint.types"
import { API_ENDPOINTS } from "@/api/generated/endpoints"
import { TVUpdates_ActionResponse, TVUpdates_SettingsView, TVUpdates_StatusResponse } from "@/api/generated/types"
import { describeActionResult, tvUpdateErrorMessage } from "@/lib/tv-updates/tv-updates"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

const E = API_ENDPOINTS.TV_UPDATES

/** Garceslab Anime: the TV updater settings (the token is never returned, only tokenConfigured). */
export function useGetTvUpdatesSettings() {
    return useServerQuery<TVUpdates_SettingsView>({
        endpoint: E.GetTvUpdatesSettings.endpoint,
        method: E.GetTvUpdatesSettings.methods[0],
        queryKey: [E.GetTvUpdatesSettings.key],
        muteError: true,
        retry: false,
    })
}

export function useSaveTvUpdatesSettings() {
    const qc = useQueryClient()
    return useServerMutation<TVUpdates_SettingsView, SaveTvUpdatesSettings_Variables>({
        endpoint: E.SaveTvUpdatesSettings.endpoint,
        method: E.SaveTvUpdatesSettings.methods[0],
        mutationKey: [E.SaveTvUpdatesSettings.key],
        onError: err => {
            toast.error(tvUpdateErrorMessage(getSeaErrorCode(err)))
        },
        onSuccess: async () => {
            await qc.invalidateQueries({ queryKey: [E.GetTvUpdatesSettings.key] })
            await qc.invalidateQueries({ queryKey: [E.GetTvUpdatesStatus.key] })
            toast.success("TV updater settings saved")
        },
    })
}

/** The updater's status through the server. Polls faster while an install runs. */
export function useGetTvUpdatesStatus(enabled = true) {
    return useServerQuery<TVUpdates_StatusResponse>({
        endpoint: E.GetTvUpdatesStatus.endpoint,
        method: E.GetTvUpdatesStatus.methods[0],
        queryKey: [E.GetTvUpdatesStatus.key],
        enabled,
        muteError: true,
        retry: false,
        refetchInterval: query => (query.state.data?.status?.installing ? 5_000 : 60_000),
    })
}

function useTvUpdateAction(action: "install" | "later") {
    const qc = useQueryClient()
    const ep = action === "install" ? E.InstallTvUpdate : E.DismissTvUpdate
    return useServerMutation<TVUpdates_ActionResponse>({
        endpoint: ep.endpoint,
        method: ep.methods[0],
        mutationKey: [ep.key],
        onError: err => {
            toast.error(tvUpdateErrorMessage(getSeaErrorCode(err)))
        },
        onSuccess: async data => {
            await qc.invalidateQueries({ queryKey: [E.GetTvUpdatesStatus.key] })
            if (!data) return
            const msg = describeActionResult(action, data.ok, data.result)
            if (data.ok) toast.success(msg)
            else toast.error(msg)
        },
    })
}

/** "Install on TV": the updater installs the pending build (the TV app closes). */
export function useInstallTvUpdate() {
    return useTvUpdateAction("install")
}

/** "Later": the updater dismisses the pending build. */
export function useDismissTvUpdate() {
    return useTvUpdateAction("later")
}
