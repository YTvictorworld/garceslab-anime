import type { EpisodeTags_Kind, EpisodeTags_Response } from "@/api/generated/types"

/**
 * Garceslab Anime: canon/filler/recap tags on episode cards, from
 * GET /api/v1/anime/episode-tags/{id} (MyAnimeList data).
 */
export type EpisodeTagMap = ReadonlyMap<number, EpisodeTags_Kind>

export const EPISODE_TAG_LABELS: Record<EpisodeTags_Kind, string> = {
    canon: "CANON",
    filler: "FILLER",
    recap: "RECAP",
    mixed: "MIX",
}

const KINDS = new Set<string>(["canon", "filler", "recap", "mixed"])

/**
 * The tags to show, or null: only when the server has data ("ready") and at
 * least one episode is filler, recap or mixed (an all-canon anime shows none).
 */
export function visibleEpisodeTags(response: EpisodeTags_Response | null | undefined): EpisodeTagMap | null {
    if (!response || response.status !== "ready" || !response.episodes) return null
    const map = new Map<number, EpisodeTags_Kind>()
    let nonCanon = false
    for (const [key, kind] of Object.entries(response.episodes)) {
        const n = Number(key)
        if (!Number.isInteger(n) || n <= 0 || !KINDS.has(kind)) continue
        map.set(n, kind)
        if (kind !== "canon") nonCanon = true
    }
    return nonCanon ? map : null
}

export function episodeTagFor(tags: EpisodeTagMap | null | undefined, episodeNumber: number | null | undefined): EpisodeTags_Kind | undefined {
    if (!tags || !episodeNumber) return undefined
    return tags.get(episodeNumber)
}
