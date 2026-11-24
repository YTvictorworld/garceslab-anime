// Garceslab Anime: local profiles endpoints (plan §3.11).

import { buildSeaQuery, useServerMutation, useServerQuery } from "@/api/client/requests"
import { API_ENDPOINTS } from "@/api/generated/endpoints"
import { PatchMyPlayerPrefs_Variables } from "@/api/generated/endpoint.types"
import { Avatar, PlayerPrefs, Profile, ProfileAuditEntry, ProfileImportResult, ProfileSession } from "@/api/generated/types"
import { serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { useQueryClient } from "@tanstack/react-query"
import { useAtomValue } from "jotai"
import { useCallback } from "react"
import { toast } from "sonner"

const P = API_ENDPOINTS.PROFILES
const PD = API_ENDPOINTS.PROFILES_DATA

export type CreateProfileInput = {
    name: string
    avatarId: string
    color?: string
    pin?: string
}

export type UpdateProfileInput = {
    name?: string
    avatarId?: string
    /** "" clears it */
    color?: string
}

export type ImportMode = "merge" | "replace"

export function useGetAvatarCatalog() {
    return useServerQuery<Array<Avatar>>({
        endpoint: P.GetAvatarCatalog.endpoint,
        method: P.GetAvatarCatalog.methods[0],
        queryKey: [P.GetAvatarCatalog.key],
        staleTime: Infinity,
    })
}

export function useGetProfiles(includeDeleted = false, enabled = true) {
    return useServerQuery<Array<Profile>>({
        endpoint: P.GetProfiles.endpoint,
        method: P.GetProfiles.methods[0],
        params: includeDeleted ? { deleted: true } : undefined,
        queryKey: [P.GetProfiles.key, includeDeleted],
        enabled,
    })
}

export function useGetProfileAudit(enabled = true) {
    return useServerQuery<Array<ProfileAuditEntry>>({
        endpoint: P.GetProfileAudit.endpoint,
        method: P.GetProfileAudit.methods[0],
        params: { limit: 50 },
        queryKey: [P.GetProfileAudit.key],
        enabled,
    })
}

function useInvalidateProfiles(withStatus = true) {
    const qc = useQueryClient()
    return useCallback(async () => {
        await qc.invalidateQueries({ queryKey: [P.GetProfiles.key] })
        await qc.invalidateQueries({ queryKey: [P.GetProfileAudit.key] })
        if (withStatus) await qc.invalidateQueries({ queryKey: [API_ENDPOINTS.STATUS.GetStatus.key] })
    }, [qc, withStatus])
}

/** firstRun: don't refetch /status (needsSetup would flip before the new profile is entered). */
export function useCreateProfile(firstRun = false) {
    const invalidate = useInvalidateProfiles(!firstRun)
    return useServerMutation<Profile, CreateProfileInput>({
        endpoint: P.CreateProfile.endpoint,
        method: P.CreateProfile.methods[0],
        mutationKey: [P.CreateProfile.key],
        onSuccess: async () => {
            await invalidate()
        },
    })
}

export function useUpdateProfile(id: number) {
    const invalidate = useInvalidateProfiles()
    return useServerMutation<Profile, UpdateProfileInput>({
        endpoint: P.UpdateProfile.endpoint.replace("{id}", String(id)),
        method: P.UpdateProfile.methods[0],
        mutationKey: [P.UpdateProfile.key, id],
        onSuccess: async () => {
            await invalidate()
            toast.success("Profile updated")
        },
    })
}

/** Profile endpoints with the id in the path (useServerMutation only takes a fixed endpoint). */
function useProfileIdMutation<R, V extends { id: number }>(
    key: string,
    endpointTemplate: string,
    method: "POST" | "GET" | "PATCH" | "DELETE" | "PUT",
    body: (v: V) => unknown,
    successMessage?: string,
) {
    const password = useAtomValue(serverAuthTokenAtom)
    const invalidate = useInvalidateProfiles()
    return useServerMutation<R, V>({
        endpoint: endpointTemplate,
        method,
        mutationKey: [key],
        mutationFn: async (v: V) => buildSeaQuery<R>({
            endpoint: endpointTemplate.replace("{id}", String(v.id)),
            method,
            data: body(v),
            password,
        }),
        onSuccess: async () => {
            await invalidate()
            if (successMessage) toast.success(successMessage)
        },
    })
}

export function useTrashProfile() {
    return useProfileIdMutation<boolean, { id: number }>(P.DeleteProfile.key, P.DeleteProfile.endpoint, "DELETE", () => undefined,
        "Profile moved to the trash")
}

export function useRestoreProfile() {
    return useProfileIdMutation<Profile, { id: number }>(P.RestoreProfile.key, P.RestoreProfile.endpoint, "POST", () => undefined,
        "Profile restored")
}

export function useSetProfilePin() {
    return useProfileIdMutation<boolean, { id: number, pin: string | null }>(P.SetProfilePin.key, P.SetProfilePin.endpoint, "PUT",
        v => ({ pin: v.pin }))
}

/** Select a profile. Errors (PIN, lockout) are handled by the caller. */
export function selectProfileRequest(id: number, password: string | undefined, pin?: string) {
    return buildSeaQuery<ProfileSession>({
        endpoint: P.SelectProfile.endpoint.replace("{id}", String(id)),
        method: "POST",
        data: pin ? { pin } : undefined,
        password,
    })
}

export function profileLogoutRequest(password: string | undefined) {
    return buildSeaQuery<boolean>({
        endpoint: P.ProfileLogout.endpoint,
        method: "POST",
        password,
    })
}

// Import / export. The optional profile token targets a profile that isn't the tab's
// current one (e.g. the one just created in "Add profile").

export function importAnilistRequest(id: number, username: string, mode: ImportMode, password?: string, profileToken?: string) {
    return buildSeaQuery<ProfileImportResult>({
        endpoint: PD.ImportProfileAnilist.endpoint.replace("{id}", String(id)),
        method: "POST",
        data: { username, mode },
        password,
        profileToken,
    })
}

export function importServerAccountRequest(id: number, mode: ImportMode, password?: string, profileToken?: string) {
    return buildSeaQuery<ProfileImportResult>({
        endpoint: PD.ImportProfileServerAccount.endpoint.replace("{id}", String(id)),
        method: "POST",
        data: { mode },
        password,
        profileToken,
    })
}

export function importJSONRequest(id: number, data: unknown, mode: ImportMode, password?: string) {
    return buildSeaQuery<ProfileImportResult>({
        endpoint: PD.ImportProfileJSON.endpoint.replace("{id}", String(id)),
        method: "POST",
        data: { mode, data },
        password,
    })
}

export function exportProfileRequest(id: number, password?: string) {
    return buildSeaQuery<unknown>({
        endpoint: PD.ExportProfile.endpoint.replace("{id}", String(id)),
        method: "GET",
        password,
    })
}

export function describeImportResult(r: ProfileImportResult | undefined): string {
    if (!r) return "Import finished"
    const parts = [`${r.imported} imported`]
    if (r.updated) parts.push(`${r.updated} updated`)
    if (r.skipped) parts.push(`${r.skipped} skipped`)
    return "Import finished: " + parts.join(", ")
}

// Player preferences of the tab's profile (phase 4). Sent with the tab's profile token.

export function getMyPlayerPrefsRequest(password?: string) {
    return buildSeaQuery<PlayerPrefs>({
        endpoint: API_ENDPOINTS.PROFILES_PLAYER.GetMyPlayerPrefs.endpoint,
        method: "GET",
        password,
    })
}

export function patchMyPlayerPrefsRequest(patch: PatchMyPlayerPrefs_Variables, password?: string) {
    return buildSeaQuery<PlayerPrefs, PatchMyPlayerPrefs_Variables>({
        endpoint: API_ENDPOINTS.PROFILES_PLAYER.PatchMyPlayerPrefs.endpoint,
        method: "PATCH",
        data: patch,
        password,
    })
}
