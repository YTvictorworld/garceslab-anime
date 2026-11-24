import { useProfileSwitcher } from "@/app/(main)/_features/profiles/use-profile-switcher"
import { ProfileAvatar } from "@/app/(main)/_features/profiles/profile-avatar"
import { useServerStatus } from "@/app/(main)/_hooks/use-server-status"
import { cn } from "@/components/ui/core/styling"
import { DropdownMenu, DropdownMenuItem } from "@/components/ui/dropdown-menu"
import { useRouter } from "@/lib/navigation"
import React from "react"
import { LuArrowLeftRight, LuUsers } from "react-icons/lu"

/** The current profile, or undefined on servers without profiles. */
export function useCurrentProfile() {
    const status = useServerStatus()
    return status?.profiles?.enabled ? status.currentProfile : undefined
}

/** Back to "Who's watching?". The URL stays, so the new profile lands on the same page. */
export function useSwitchProfile() {
    const { leave } = useProfileSwitcher()
    return React.useCallback(() => void leave(), [leave])
}

type ProfileMenuProps = {
    /** Show the name next to the avatar. */
    showName?: boolean
    className?: string
    avatarClass?: string
}

/** Current profile avatar with "Switch profile" and "Manage profiles" (plan §3.9.4). */
export function ProfileMenu({ showName, className, avatarClass }: ProfileMenuProps) {
    const profile = useCurrentProfile()
    const switchProfile = useSwitchProfile()
    const router = useRouter()

    if (!profile) return null

    return (
        <DropdownMenu
            trigger={<button
                type="button"
                data-profile-menu-trigger
                className={cn("flex items-center gap-3 rounded-[--radius-md] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/40", className)}
                aria-label={`Profile: ${profile.name}`}
            >
                <ProfileAvatar url={profile.avatarUrl} name={profile.name} color={profile.color} className={cn("w-8", avatarClass)} />
                {showName && <span className="truncate text-sm text-[--muted]">{profile.name}</span>}
            </button>}
        >
            <div data-profile-menu-header className="flex items-center gap-3 px-2 py-2">
                <ProfileAvatar url={profile.avatarUrl} name={profile.name} color={profile.color} className="w-10" />
                <div className="min-w-0">
                    <p className="text-sm font-semibold truncate">{profile.name}</p>
                    <p className="text-xs text-[--muted]">Current profile</p>
                </div>
            </div>
            <DropdownMenuItem onClick={switchProfile} data-switch-profile>
                <LuArrowLeftRight /> Switch profile
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => router.push("/settings?tab=profiles")}>
                <LuUsers /> Manage profiles
            </DropdownMenuItem>
        </DropdownMenu>
    )
}
