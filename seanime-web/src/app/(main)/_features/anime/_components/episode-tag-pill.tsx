import { EpisodeTags_Kind } from "@/api/generated/types"
import { cn } from "@/components/ui/core/styling"
import { EPISODE_TAG_LABELS } from "@/lib/episode-tags/episode-tags"
import React from "react"

const episodeTagClassNames: Record<EpisodeTags_Kind, string> = {
    // Muted lilac (the brand's 300 shade), outlined.
    canon: "bg-[rgba(20,12,28,0.72)] text-[#e6b0fd] border border-[rgba(230,176,253,0.45)]",
    // Warm orange: the one to notice.
    filler: "bg-[#f59e4b] text-[#1c0d02]",
    recap: "bg-[rgba(120,120,134,0.92)] text-[#f4f4f6]",
    mixed: "bg-gradient-to-r from-[#a443cf] to-[#f59e4b] text-white",
}

/**
 * Garceslab Anime: the CANON / FILLER / RECAP / MIX pill (see useEpisodeTags).
 * Episode cards place it with `className`.
 */
export function EpisodeTagPill({ kind, className, ...rest }: { kind: EpisodeTags_Kind } & React.ComponentPropsWithoutRef<"span">) {
    return (
        <span
            data-episode-tag={kind}
            {...rest}
            className={cn(
                "inline-block pointer-events-none select-none",
                "rounded-[5px] px-1.5 py-[1px] text-[0.65rem] leading-4 font-extrabold tracking-wider",
                episodeTagClassNames[kind],
                className,
            )}
        >{EPISODE_TAG_LABELS[kind]}</span>
    )
}
