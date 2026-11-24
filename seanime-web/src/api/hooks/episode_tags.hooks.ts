import { useServerQuery } from "@/api/client/requests"
import { API_ENDPOINTS } from "@/api/generated/endpoints"
import { EpisodeTags_Kind, EpisodeTags_Response, Nullish } from "@/api/generated/types"
import { episodeTagFor, EpisodeTagMap, visibleEpisodeTags } from "@/lib/episode-tags/episode-tags"
import React from "react"

const POLL_MS = 4000
const MAX_POLLS = 30

/**
 * Garceslab Anime: canon/filler/recap tags for an anime's episode cards, or
 * null when there is nothing to show. Errors are muted (an older server, no
 * data): the cards just have no tag. While the server fetches ("pending") it
 * asks again every 4 s for about two minutes.
 */
export function useEpisodeTags(mediaId: Nullish<number>): EpisodeTagMap | null {
    const { data } = useServerQuery<EpisodeTags_Response>({
        endpoint: API_ENDPOINTS.ANIME_EPISODE_TAGS.GetAnimeEpisodeTags.endpoint.replace("{id}", String(mediaId)),
        method: API_ENDPOINTS.ANIME_EPISODE_TAGS.GetAnimeEpisodeTags.methods[0],
        queryKey: [API_ENDPOINTS.ANIME_EPISODE_TAGS.GetAnimeEpisodeTags.key, String(mediaId)],
        enabled: !!mediaId,
        muteError: true,
        retry: false,
        staleTime: 30 * 60 * 1000,
        refetchOnWindowFocus: false,
        refetchInterval: query => (query.state.data?.status === "pending" && query.state.dataUpdateCount < MAX_POLLS ? POLL_MS : false),
    })
    return React.useMemo(() => visibleEpisodeTags(data), [data])
}

/**
 * One episode's tag (continue watching, the autoplay prompt), from the same
 * cache as the anime page. Pass a null mediaId to skip the request.
 */
export function useEpisodeTag(mediaId: Nullish<number>, episodeNumber: Nullish<number>): EpisodeTags_Kind | undefined {
    const tags = useEpisodeTags(episodeNumber ? mediaId : null)
    return episodeTagFor(tags, episodeNumber)
}
