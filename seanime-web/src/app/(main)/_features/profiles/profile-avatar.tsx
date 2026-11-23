import { getServerBaseUrl } from "@/api/client/server-url"
import { cn } from "@/components/ui/core/styling"
import React from "react"
import { LuUser } from "react-icons/lu"

/** URL of a bundled avatar (/profile-avatars/<file>), on the server's origin. */
export function profileAvatarSrc(url: string | undefined | null): string | undefined {
    if (!url) return undefined
    if (/^https?:\/\//.test(url)) return url
    return getServerBaseUrl() + url
}

type ProfileAvatarProps = {
    url?: string | null
    name?: string
    color?: string | null
    className?: string
}

/** Square profile picture with an optional colour ring. */
export function ProfileAvatar({ url, name, color, className }: ProfileAvatarProps) {
    const [failed, setFailed] = React.useState(false)
    const src = profileAvatarSrc(url)

    React.useEffect(() => setFailed(false), [src])

    return (
        <div
            data-profile-avatar
            className={cn(
                "relative aspect-square overflow-hidden rounded-[--radius-md] bg-[--subtle] flex items-center justify-center shrink-0",
                color && "ring-2 ring-offset-2 ring-offset-[--background]",
                className,
            )}
            style={color ? { ["--tw-ring-color" as string]: color } : undefined}
        >
            {src && !failed ? (
                <img
                    src={src}
                    alt={name ? `${name}'s avatar` : "Avatar"}
                    className="w-full h-full object-cover"
                    draggable={false}
                    onError={() => setFailed(true)}
                />
            ) : (
                <LuUser className="w-1/2 h-1/2 text-[--muted]" />
            )}
        </div>
    )
}
