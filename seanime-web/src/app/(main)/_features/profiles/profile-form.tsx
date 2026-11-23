import { Avatar } from "@/api/generated/types"
import { useGetAvatarCatalog } from "@/api/hooks/profiles.hooks"
import { AvatarPicker } from "@/app/(main)/_features/profiles/avatar-picker"
import { ProfileAvatar } from "@/app/(main)/_features/profiles/profile-avatar"
import { cn } from "@/components/ui/core/styling"
import { TextInput } from "@/components/ui/text-input"
import React from "react"

export const PIN_NOTICE = "The PIN stops others entering your profile by mistake. It doesn't protect the data."

export const PROFILE_NAME_MAX = 32

// Optional accent colours for a profile (user data, not theme tokens).
export const PROFILE_COLORS = ["#e11d48", "#f97316", "#eab308", "#22c55e", "#14b8a6", "#3b82f6", "#8b5cf6", "#ec4899"]

export function isValidPin(pin: string) {
    return /^\d{4,6}$/.test(pin)
}

type ProfileIdentityFieldsProps = {
    name: string
    onNameChange: (v: string) => void
    avatarId: string
    onAvatarChange: (a: Avatar) => void
    color: string
    onColorChange: (c: string) => void
    autoFocus?: boolean
}

/** Name, avatar and colour (create and edit). */
export function ProfileIdentityFields(props: ProfileIdentityFieldsProps) {
    const { name, onNameChange, avatarId, onAvatarChange, color, onColorChange, autoFocus } = props
    const { data: catalog } = useGetAvatarCatalog()
    const selected = catalog?.find(a => a.id === avatarId)

    return (
        <div className="space-y-5">
            <div className="flex items-center gap-4">
                <ProfileAvatar url={selected?.url} name={name} color={color || undefined} className="w-20" />
                <TextInput
                    label="Name"
                    value={name}
                    maxLength={PROFILE_NAME_MAX}
                    onValueChange={onNameChange}
                    autoFocus={autoFocus}
                    placeholder="Your name"
                    data-profile-name-input
                />
            </div>
            <div className="space-y-2">
                <p className="text-sm font-semibold">Colour <span className="font-normal text-[--muted]">(optional)</span></p>
                <div className="flex flex-wrap gap-2">
                    <button
                        type="button"
                        onClick={() => onColorChange("")}
                        aria-pressed={!color}
                        className={cn(
                            "size-8 rounded-full border-2 border-[--border] text-xs text-[--muted]",
                            !color && "border-white",
                        )}
                    >—</button>
                    {PROFILE_COLORS.map(c => (
                        <button
                            key={c}
                            type="button"
                            onClick={() => onColorChange(c)}
                            aria-label={`Colour ${c}`}
                            aria-pressed={color === c}
                            className={cn("size-8 rounded-full border-2 border-transparent", color === c && "border-white")}
                            style={{ backgroundColor: c }}
                        />
                    ))}
                </div>
            </div>
            <AvatarPicker value={avatarId} onChange={onAvatarChange} />
        </div>
    )
}

type PinFieldsProps = {
    pin: string
    confirm: string
    onPinChange: (v: string) => void
    onConfirmChange: (v: string) => void
}

/** New PIN + confirmation. */
export function PinFields({ pin, confirm, onPinChange, onConfirmChange }: PinFieldsProps) {
    const digits = (v: string) => v.replace(/\D/g, "").slice(0, 6)
    const mismatch = confirm.length > 0 && pin !== confirm
    return (
        <div className="space-y-2">
            <div className="grid grid-cols-2 gap-3">
                <TextInput
                    label="PIN (4-6 digits)"
                    type="password"
                    inputMode="numeric"
                    autoComplete="off"
                    value={pin}
                    onValueChange={v => onPinChange(digits(v))}
                    data-profile-pin-input
                />
                <TextInput
                    label="Repeat the PIN"
                    type="password"
                    inputMode="numeric"
                    autoComplete="off"
                    value={confirm}
                    onValueChange={v => onConfirmChange(digits(v))}
                    error={mismatch ? "The PINs don't match" : undefined}
                    data-profile-pin-confirm-input
                />
            </div>
            <p className="text-xs text-[--muted]">{PIN_NOTICE}</p>
        </div>
    )
}
