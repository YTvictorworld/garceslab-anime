import { useIsAnimeFavourite, useToggleFavourite } from "@/api/hooks/profiles_library.hooks"
import { useCurrentProfile } from "@/app/(main)/_features/profiles/profile-menu"
import { IconButton } from "@/components/ui/button"
import { cn } from "@/components/ui/core/styling"
import { Tooltip } from "@/components/ui/tooltip"
import React from "react"
import { AiFillHeart, AiOutlineHeart } from "react-icons/ai"

/** Garceslab Anime: favourite toggle for an anime, stored in the current profile (phase 3). */
export function ProfileFavouriteButton({ mediaId, className }: { mediaId: number | undefined, className?: string }) {
    const profile = useCurrentProfile()
    const { isFavourite, isLoading } = useIsAnimeFavourite(mediaId, !!profile)
    const { mutate, isPending } = useToggleFavourite()

    if (!profile || !mediaId) return null

    return <Tooltip
        trigger={<IconButton
            data-profile-favourite-button
            intent={isFavourite ? "alert-subtle" : "gray-subtle"}
            icon={isFavourite ? <AiFillHeart /> : <AiOutlineHeart />}
            rounded
            size="sm"
            aria-pressed={isFavourite}
            aria-label={isFavourite ? "Remove from favourites" : "Add to favourites"}
            loading={isPending}
            disabled={isLoading}
            className={cn(className)}
            onClick={() => mutate({ kind: "ANIME", refId: mediaId, favourite: !isFavourite })}
        />}
    >
        {isFavourite ? "Remove from favourites" : "Add to favourites"}
    </Tooltip>
}
