import { Avatar } from "@/api/generated/types"
import { useGetAvatarCatalog } from "@/api/hooks/profiles.hooks"
import { ProfileAvatar } from "@/app/(main)/_features/profiles/profile-avatar"
import { cn } from "@/components/ui/core/styling"
import { LoadingSpinner } from "@/components/ui/loading-spinner"
import React from "react"

const CATEGORY_LABELS: Record<string, string> = {
    characters: "Characters",
    creatures: "Creatures",
    fun: "Fun",
    retro: "Retro",
    space: "Space",
}

/** Credits the CC BY avatars need (one line per artist and style). */
export function avatarCredits(avatars: Avatar[] | undefined): string[] {
    return [...new Set((avatars ?? []).map(a => a.attribution).filter((a): a is string => !!a))]
}

function categoryLabel(c: string) {
    return CATEGORY_LABELS[c] ?? (c.charAt(0).toUpperCase() + c.slice(1))
}

/** Avatars grouped by category, in catalog order. */
export function groupAvatars(avatars: Avatar[] | undefined): Array<[string, Avatar[]]> {
    const groups = new Map<string, Avatar[]>()
    for (const a of avatars ?? []) {
        const list = groups.get(a.category) ?? []
        list.push(a)
        groups.set(a.category, list)
    }
    return [...groups.entries()]
}

type AvatarPickerProps = {
    value?: string
    onChange: (avatar: Avatar) => void
}

/** Grid of the bundled avatars by category (no uploads, plan §3.1.1). */
export function AvatarPicker({ value, onChange }: AvatarPickerProps) {
    const { data: catalog, isLoading } = useGetAvatarCatalog()
    const groups = React.useMemo(() => groupAvatars(catalog), [catalog])

    if (isLoading) return <LoadingSpinner />

    return (
        <div data-avatar-picker className="space-y-4">
            {groups.map(([category, avatars]) => (
                <div key={category} className="space-y-2">
                    <p className="text-sm font-semibold text-[--muted]">{categoryLabel(category)}</p>
                    <div className="grid grid-cols-4 sm:grid-cols-6 md:grid-cols-7 gap-2">
                        {avatars.map(a => (
                            <button
                                key={a.id}
                                type="button"
                                title={a.attribution ? `${a.label}. ${a.attribution}` : a.label}
                                aria-label={a.label}
                                aria-pressed={value === a.id}
                                data-avatar-id={a.id}
                                onClick={() => onChange(a)}
                                className={cn(
                                    "rounded-[--radius-md] p-0.5 border-2 border-transparent transition hover:scale-105 focus-visible:outline-none focus-visible:border-[--brand]",
                                    value === a.id && "border-[--brand]",
                                )}
                            >
                                <ProfileAvatar url={a.url} name={a.label} />
                            </button>
                        ))}
                    </div>
                </div>
            ))}
            <div data-avatar-credits className="text-xs text-[--muted] space-y-1">
                <p>Avatars made with DiceBear styles under CC0 1.0 or CC BY 4.0 (see LICENSES.md).</p>
                {avatarCredits(catalog).map(line => <p key={line}>{line}</p>)}
            </div>
        </div>
    )
}
