import { buildSeaQuery } from "@/api/client/requests"
import { API_ENDPOINTS } from "@/api/generated/endpoints"
import { AL_BaseAnime, AL_ListAnime } from "@/api/generated/types"
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

/**
 * Suggestions while typing (release 1.0 "search as you type"). Uses the public AniList
 * search the server proxies (POST /anilist/list-anime, no profile needed). Changing the
 * query aborts the previous request (React Query's signal) and results are cached.
 */
export function useAnimeSuggestions(query: string) {
    const password = useAtomValue(serverAuthTokenAtom)
    const debounced = useDebounce(query.trim(), TYPEAHEAD_DEBOUNCE_MS)
    const enabled = debounced.length >= TYPEAHEAD_MIN_CHARS

    const q = useQuery({
        queryKey: ["search-typeahead", debounced],
        enabled,
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

    const media = React.useMemo(
        () => (enabled ? (q.data?.Page?.media?.filter(Boolean) as AL_BaseAnime[] | undefined) : undefined) ?? [],
        [q.data, enabled],
    )
    return { media, isFetching: enabled && q.isFetching, enabled, debounced }
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
    const { media, isFetching, enabled } = useAnimeSuggestions(query)
    const [highlighted, setHighlighted] = React.useState(-1)
    const listId = React.useId()

    React.useEffect(() => setHighlighted(-1), [media])

    const visible = open && query.trim().length >= TYPEAHEAD_MIN_CHARS && (media.length > 0 || isFetching || enabled)

    const select = React.useCallback((m: AL_BaseAnime) => {
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
                    {media.map((m, i) => {
                        const year = m.seasonYear ?? m.startDate?.year
                        const alt = m.title?.english && m.title.english !== m.title?.userPreferred ? m.title.english : m.title?.romaji
                        return (
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
                                    {!!m.coverImage?.medium && <SeaImage
                                        src={m.coverImage.medium}
                                        alt=""
                                        fill
                                        sizes="36px"
                                        className="object-cover"
                                    />}
                                </div>
                                <div className="min-w-0">
                                    <p className="truncate text-sm font-medium">{m.title?.userPreferred || m.title?.romaji}</p>
                                    <p className="truncate text-xs text-[--muted]">
                                        {[alt !== m.title?.userPreferred ? alt : null, m.format?.replace(/_/g, " "), year].filter(Boolean).join(" · ")}
                                    </p>
                                </div>
                            </li>
                        )
                    })}
                </ul>
            )}
        </div>
    )
}
