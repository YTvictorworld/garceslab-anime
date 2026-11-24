import { buildSeaQuery } from "@/api/client/requests"
import { API_ENDPOINTS } from "@/api/generated/endpoints"
import { AL_BaseAnime, AL_ListAnime, TitleIndex_Suggestion } from "@/api/generated/types"
import { serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { SeaImage } from "@/components/shared/sea-image"
import { cn } from "@/components/ui/core/styling"
import { LoadingSpinner } from "@/components/ui/loading-spinner"
import { useDebounce } from "@/hooks/use-debounce"
import { useRouter } from "@/lib/navigation"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { useAtomValue } from "jotai"
import React from "react"

export const TYPEAHEAD_MIN_CHARS = 2
const TYPEAHEAD_DEBOUNCE_MS = 250
const TYPEAHEAD_LIMIT = 8

/** One row of the dropdown, from the title index or from AniList. */
export type TypeaheadItem = {
    id: number
    title: string
    /** Second line: the matched synonym or the English title, format and year. */
    detail: string
    image?: string
}

function fromIndex(s: TitleIndex_Suggestion): TypeaheadItem {
    return {
        id: s.mediaId,
        title: s.title,
        detail: [s.matched, s.format?.replace(/_/g, " "), s.year].filter(Boolean).join(" · "),
        image: s.thumbnail || s.picture,
    }
}

function fromAnilist(m: AL_BaseAnime): TypeaheadItem {
    const title = m.title?.userPreferred || m.title?.romaji || ""
    const year = m.seasonYear ?? m.startDate?.year
    const alt = m.title?.english && m.title.english !== m.title?.userPreferred ? m.title.english : m.title?.romaji
    return {
        id: m.id,
        title,
        detail: [alt !== title ? alt : null, m.format?.replace(/_/g, " "), year].filter(Boolean).join(" · "),
        image: m.coverImage?.medium,
    }
}

/**
 * Suggestions while typing (release 1.0 "search as you type"). First the server's
 * title index (GET /search/suggest: prefix matches, "ble" finds Bleach); when it has
 * nothing (not downloaded yet, disabled, or no match) the public AniList search the
 * server proxies (POST /anilist/list-anime, whole words only). Neither needs a
 * profile. Changing the query aborts the previous requests (React Query's signal)
 * and results are cached.
 */
export function useAnimeSuggestions(query: string) {
    const password = useAtomValue(serverAuthTokenAtom)
    const debounced = useDebounce(query.trim(), TYPEAHEAD_DEBOUNCE_MS)
    const enabled = debounced.length >= TYPEAHEAD_MIN_CHARS

    const indexQuery = useQuery({
        queryKey: ["search-typeahead-index", debounced],
        enabled,
        staleTime: 5 * 60 * 1000,
        placeholderData: keepPreviousData,
        retry: false,
        queryFn: ({ signal }) => buildSeaQuery<TitleIndex_Suggestion[] | undefined, { q: string, limit: number }>({
            endpoint: API_ENDPOINTS.SEARCH_SUGGEST.SearchSuggest.endpoint,
            method: "GET",
            params: { q: debounced, limit: TYPEAHEAD_LIMIT },
            password,
            signal,
        }),
    })
    // Fall back to AniList once the index answered nothing for this query (an older
    // server without the endpoint fails the request, which counts as nothing).
    const indexSettled = !indexQuery.isFetching && !indexQuery.isPlaceholderData
    const useAnilist = enabled && indexSettled && (indexQuery.isError || !indexQuery.data?.length)

    const anilistQuery = useQuery({
        queryKey: ["search-typeahead", debounced],
        enabled: useAnilist,
        staleTime: 5 * 60 * 1000,
        placeholderData: keepPreviousData,
        retry: false,
        queryFn: ({ signal }) => buildSeaQuery<AL_ListAnime>({
            endpoint: API_ENDPOINTS.ANILIST.AnilistListAnime.endpoint,
            method: "POST",
            data: {
                search: debounced,
                page: 1,
                perPage: TYPEAHEAD_LIMIT,
                sort: ["SEARCH_MATCH"],
                status: ["FINISHED", "CANCELLED", "NOT_YET_RELEASED", "RELEASING"],
            },
            password,
            signal,
        }),
    })

    const items = React.useMemo(() => {
        if (!enabled) return []
        if (useAnilist) return ((anilistQuery.data?.Page?.media?.filter(Boolean) ?? []) as AL_BaseAnime[]).map(fromAnilist)
        // While the index runs, the previous query's rows stay up (keepPreviousData).
        return (indexQuery.data ?? []).map(fromIndex)
    }, [enabled, useAnilist, anilistQuery.data, indexQuery.data])

    const isFetching = enabled && (indexQuery.isFetching || (useAnilist && anilistQuery.isFetching))
    return { items, isFetching, enabled, debounced }
}

type SearchTypeaheadProps = {
    query: string
    open: boolean
    onOpenChange: (open: boolean) => void
    /** The input the dropdown belongs to (keyboard navigation is bound to it). */
    inputRef: React.RefObject<HTMLInputElement | null>
}

/** Dropdown of matching anime under the search input (↑/↓ to move, Enter to open, Esc to close). */
export function SearchTypeahead({ query, open, onOpenChange, inputRef }: SearchTypeaheadProps) {
    const router = useRouter()
    const { items: media, isFetching, enabled } = useAnimeSuggestions(query)
    const [highlighted, setHighlighted] = React.useState(-1)
    const listId = React.useId()

    React.useEffect(() => setHighlighted(-1), [media])

    const visible = open && query.trim().length >= TYPEAHEAD_MIN_CHARS && (media.length > 0 || isFetching || enabled)

    const select = React.useCallback((m: TypeaheadItem) => {
        onOpenChange(false)
        router.push(`/entry?id=${m.id}`)
    }, [onOpenChange, router])

    React.useEffect(() => {
        const input = inputRef.current
        if (!input) return
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === "Escape") {
                if (open) {
                    e.preventDefault()
                    onOpenChange(false)
                }
                return
            }
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                if (!media.length) return
                e.preventDefault()
                if (!open) onOpenChange(true)
                setHighlighted(i => {
                    if (e.key === "ArrowDown") return i + 1 >= media.length ? 0 : i + 1
                    return i <= 0 ? media.length - 1 : i - 1
                })
                return
            }
            if (e.key === "Enter" && open && highlighted >= 0 && media[highlighted]) {
                e.preventDefault()
                select(media[highlighted])
            }
        }
        input.addEventListener("keydown", onKeyDown)
        input.setAttribute("aria-controls", listId)
        input.setAttribute("aria-expanded", String(visible))
        input.setAttribute("aria-autocomplete", "list")
        if (highlighted >= 0) input.setAttribute("aria-activedescendant", `${listId}-${highlighted}`)
        else input.removeAttribute("aria-activedescendant")
        return () => input.removeEventListener("keydown", onKeyDown)
    }, [inputRef, open, media, highlighted, select, onOpenChange, listId, visible])

    if (!visible) return null

    return (
        <div
            data-search-typeahead
            className="absolute left-0 right-0 top-full mt-1 z-50 rounded-[--radius-md] border border-[--border] bg-[--paper] shadow-lg overflow-hidden"
            // Keep focus in the input when clicking a suggestion.
            onMouseDown={e => e.preventDefault()}
        >
            {media.length === 0 && isFetching && <LoadingSpinner className="h-16" />}
            {media.length === 0 && !isFetching && <p className="p-3 text-sm text-[--muted]">No matches</p>}
            {media.length > 0 && (
                <ul id={listId} role="listbox" className="max-h-[60vh] overflow-y-auto py-1">
                    {media.map((m, i) => (
                        <li
                            key={m.id}
                            id={`${listId}-${i}`}
                            role="option"
                            aria-selected={i === highlighted}
                            data-typeahead-item={m.id}
                            onMouseEnter={() => setHighlighted(i)}
                            onClick={() => select(m)}
                            className={cn(
                                "flex items-center gap-3 px-3 py-2 cursor-pointer",
                                i === highlighted && "bg-[--subtle]",
                            )}
                        >
                            <div className="relative w-9 h-12 shrink-0 overflow-hidden rounded-sm bg-[--subtle]">
                                {!!m.image && <SeaImage
                                    src={m.image}
                                    alt=""
                                    fill
                                    sizes="36px"
                                    className="object-cover"
                                />}
                            </div>
                            <div className="min-w-0">
                                <p className="truncate text-sm font-medium">{m.title}</p>
                                <p className="truncate text-xs text-[--muted]">{m.detail}</p>
                            </div>
                        </li>
                    ))}
                </ul>
            )}
        </div>
    )
}
