// Garceslab Anime phase 3: favourites, the full entry editor, custom lists and
// "recently watched" of the current profile (plan §3.11).

import { buildSeaQuery, useServerMutation, useServerQuery } from "@/api/client/requests"
import { API_ENDPOINTS } from "@/api/generated/endpoints"
import {
    ProfileCustomList,
    ProfileEntryDetails,
    ProfileFavourite,
    ProfileFuzzyDate,
    ProfileRecentlyWatched,
} from "@/api/generated/types"
import { serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { useQueryClient } from "@tanstack/react-query"
import { useAtomValue } from "jotai"
import { useCallback } from "react"
import { toast } from "sonner"

const PL = API_ENDPOINTS.PROFILES_LIBRARY
const PD = API_ENDPOINTS.PROFILES_DATA

export type FavouriteKind = "ANIME" | "MANGA" | "CHARACTER" | "STAFF" | "STUDIO"

/** A partial entry edit: only the fields sent change. */
export type ProfileEntryEdit = {
    status?: string
    /** 0-100, 0 clears the score */
    scoreRaw?: number
    progress?: number
    repeat?: number
    private?: boolean
    hiddenFromStatusLists?: boolean
    /** "" clears the notes */
    notes?: string
    /** a date with null parts clears it */
    startedAt?: ProfileFuzzyDate
    completedAt?: ProfileFuzzyDate
    /** replaces the custom list membership */
    customListIds?: number[]
}

/** Queries that show the list: refetched after any list change. */
export function useInvalidateProfileLists() {
    const qc = useQueryClient()
    return useCallback(async (mediaId?: number) => {
        const keys = [
            PL.GetMyListEntry.key,
            PD.GetMyCustomLists.key,
            PL.GetMyRecentlyWatched.key,
            API_ENDPOINTS.ANIME_COLLECTION.GetLibraryCollection.key,
            API_ENDPOINTS.ANILIST.GetAnimeCollection.key,
            API_ENDPOINTS.ANILIST.GetRawAnimeCollection.key,
            API_ENDPOINTS.ANILIST.GetRawAnimeCollectionTags.key,
            API_ENDPOINTS.ANILIST.GetAniListStats.key,
        ]
        await Promise.all(keys.map(key => qc.invalidateQueries({ queryKey: [key] })))
        if (mediaId) await qc.invalidateQueries({ queryKey: [API_ENDPOINTS.ANIME_ENTRIES.GetAnimeEntry.key, String(mediaId)] })
    }, [qc])
}

//----------------------------------------------------------------------------------------------------------------------
// Favourites

export function useGetMyFavourites(kind: FavouriteKind | undefined, enabled = true) {
    return useServerQuery<Array<ProfileFavourite>>({
        endpoint: PL.GetMyFavourites.endpoint,
        method: "GET",
        params: kind ? { kind } : undefined,
        queryKey: [PL.GetMyFavourites.key, kind ?? "ALL"],
        enabled,
    })
}

/** Whether a media is one of the current profile's anime favourites. */
export function useIsAnimeFavourite(mediaId: number | undefined, enabled = true) {
    const { data, isLoading } = useGetMyFavourites("ANIME", enabled && !!mediaId)
    return { isFavourite: !!mediaId && !!data?.some(f => f.refId === mediaId), isLoading }
}

export function useToggleFavourite() {
    const password = useAtomValue(serverAuthTokenAtom)
    const qc = useQueryClient()
    return useServerMutation<boolean, { kind: FavouriteKind, refId: number, favourite: boolean }>({
        endpoint: PL.AddMyFavourite.endpoint,
        method: "POST",
        mutationKey: [PL.AddMyFavourite.key],
        mutationFn: async v => buildSeaQuery<boolean>({
            endpoint: PL.AddMyFavourite.endpoint.replace("{kind}", v.kind).replace("{refId}", String(v.refId)),
            method: v.favourite ? "POST" : "DELETE",
            password,
        }),
        onSuccess: async (_, v) => {
            await qc.invalidateQueries({ queryKey: [PL.GetMyFavourites.key] })
            await qc.invalidateQueries({ queryKey: [PL.GetMyListEntry.key] })
            toast.success(v.favourite ? "Added to favourites" : "Removed from favourites")
        },
    })
}

//----------------------------------------------------------------------------------------------------------------------
// Entry editor

export function useGetMyListEntry(mediaId: number | undefined, enabled = true) {
    return useServerQuery<ProfileEntryDetails | null>({
        endpoint: PL.GetMyListEntry.endpoint.replace("{mediaId}", String(mediaId)),
        method: "GET",
        queryKey: [PL.GetMyListEntry.key, String(mediaId)],
        enabled: enabled && !!mediaId,
    })
}

export function useEditMyListEntry(mediaId: number | undefined, onSuccess?: () => void) {
    const password = useAtomValue(serverAuthTokenAtom)
    const invalidate = useInvalidateProfileLists()
    return useServerMutation<ProfileEntryDetails, ProfileEntryEdit>({
        endpoint: PL.EditMyListEntry.endpoint,
        method: "PATCH",
        mutationKey: [PL.EditMyListEntry.key, String(mediaId)],
        mutationFn: async v => buildSeaQuery<ProfileEntryDetails>({
            endpoint: PL.EditMyListEntry.endpoint.replace("{mediaId}", String(mediaId)),
            method: "PATCH",
            data: v,
            password,
        }),
        onSuccess: async () => {
            await invalidate(mediaId)
            toast.success("Entry updated")
            onSuccess?.()
        },
    })
}

//----------------------------------------------------------------------------------------------------------------------
// Custom lists

export function useGetMyCustomLists(enabled = true) {
    return useServerQuery<Array<ProfileCustomList>>({
        endpoint: PD.GetMyCustomLists.endpoint,
        method: "GET",
        queryKey: [PD.GetMyCustomLists.key],
        enabled,
    })
}

export function useCreateMyCustomList() {
    const invalidate = useInvalidateProfileLists()
    return useServerMutation<Array<ProfileCustomList>, { name: string }>({
        endpoint: PD.CreateMyCustomList.endpoint,
        method: "POST",
        mutationKey: [PD.CreateMyCustomList.key],
        onSuccess: async () => {
            await invalidate()
        },
    })
}

export function useUpdateMyCustomList() {
    const password = useAtomValue(serverAuthTokenAtom)
    const invalidate = useInvalidateProfileLists()
    return useServerMutation<Array<ProfileCustomList>, { id: number, name?: string, position?: number }>({
        endpoint: PL.UpdateMyCustomList.endpoint,
        method: "PATCH",
        mutationKey: [PL.UpdateMyCustomList.key],
        mutationFn: async ({ id, ...body }) => buildSeaQuery<Array<ProfileCustomList>>({
            endpoint: PL.UpdateMyCustomList.endpoint.replace("{listId}", String(id)),
            method: "PATCH",
            data: body,
            password,
        }),
        onSuccess: async () => {
            await invalidate()
        },
    })
}

export function useDeleteMyCustomList() {
    const password = useAtomValue(serverAuthTokenAtom)
    const invalidate = useInvalidateProfileLists()
    return useServerMutation<boolean, { id: number }>({
        endpoint: PD.DeleteMyCustomList.endpoint,
        method: "DELETE",
        mutationKey: [PD.DeleteMyCustomList.key],
        mutationFn: async ({ id }) => buildSeaQuery<boolean>({
            endpoint: PD.DeleteMyCustomList.endpoint.replace("{listId}", String(id)),
            method: "DELETE",
            password,
        }),
        onSuccess: async () => {
            await invalidate()
            toast.success("List deleted")
        },
    })
}

//----------------------------------------------------------------------------------------------------------------------
// Recently watched

export function useGetMyRecentlyWatched(limit = 20, enabled = true) {
    return useServerQuery<Array<ProfileRecentlyWatched>>({
        endpoint: PL.GetMyRecentlyWatched.endpoint,
        method: "GET",
        params: { limit },
        queryKey: [PL.GetMyRecentlyWatched.key, limit],
        enabled,
    })
}
