import { getProfileErrorMessage, getSeaErrorCode } from "@/api/client/requests"
import { Profile } from "@/api/generated/types"
import { selectProfileRequest, useGetProfiles } from "@/api/hooks/profiles.hooks"
import { serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { CreateProfileScreen } from "@/app/(main)/_features/profiles/create-profile-screen"
import { PinPad } from "@/app/(main)/_features/profiles/pin-pad"
import { ProfileAvatar } from "@/app/(main)/_features/profiles/profile-avatar"
import { useProfileSwitcher } from "@/app/(main)/_features/profiles/use-profile-switcher"
import { LuffyError } from "@/components/shared/luffy-error"
import { Button } from "@/components/ui/button"
import { LoadingSpinner } from "@/components/ui/loading-spinner"
import { Modal } from "@/components/ui/modal"
import axios from "axios"
import { useAtomValue } from "jotai"
import React from "react"
import { LuLock, LuPlus } from "react-icons/lu"
import { toast } from "sonner"

/** "Who's watching?" (plan §3.9.1), shown on every app load. */
export function WhosWatching() {
    const password = useAtomValue(serverAuthTokenAtom)
    const { data: profiles, isLoading, isError, refetch } = useGetProfiles()
    const { enter, resume } = useProfileSwitcher()

    const [adding, setAdding] = React.useState(false)
    const [pinProfile, setPinProfile] = React.useState<Profile | null>(null)
    const [pendingId, setPendingId] = React.useState<number | null>(null)

    async function pick(p: Profile) {
        if (pendingId) return
        setPendingId(p.id)
        try {
            // Same tab, same profile: no PIN again after a reload.
            if (await resume(p.id)) return
            if (p.hasPin) {
                setPinProfile(p)
                return
            }
            const session = await selectProfileRequest(p.id, password)
            if (!session) throw new Error()
            await enter({ profileToken: session.profileToken, expiresAt: session.expiresAt, profileId: p.id })
        }
        catch (err) {
            const code = getSeaErrorCode(err)
            if (code === "PROFILE_PIN_REQUIRED") {
                setPinProfile(p)
            } else {
                toast.error(getProfileErrorMessage(code) || "Couldn't enter the profile")
                void refetch()
            }
        }
        finally {
            setPendingId(null)
        }
    }

    if (adding) {
        return <CreateProfileScreen
            onCancel={() => setAdding(false)}
            onCreated={() => {
                setAdding(false)
                void refetch()
            }}
        />
    }

    return (
        <div data-whos-watching className="min-h-screen w-full flex flex-col items-center justify-center px-4 py-10">
            <img src="/garceslab-logo.png" alt="logo" className="w-14 h-auto mb-6" />
            <h1 className="text-3xl md:text-5xl font-bold text-center mb-10">Who's watching?</h1>

            {isLoading && <LoadingSpinner />}
            {isError && <LuffyError title="Couldn't load the profiles">
                <Button intent="gray-outline" onClick={() => refetch()}>Retry</Button>
            </LuffyError>}

            {!!profiles && (
                <div className="flex flex-wrap justify-center gap-6 md:gap-8 max-w-5xl">
                    {profiles.map(p => (
                        <button
                            key={p.id}
                            type="button"
                            onClick={() => pick(p)}
                            disabled={pendingId !== null}
                            data-profile-card={p.name}
                            className="group flex flex-col items-center gap-3 w-28 md:w-36 focus-visible:outline-none"
                        >
                            <div className="relative w-full">
                                <ProfileAvatar
                                    url={p.avatarUrl}
                                    name={p.name}
                                    color={p.color}
                                    className="w-full transition group-hover:scale-105 group-focus-visible:scale-105 group-focus-visible:ring-2 group-focus-visible:ring-white"
                                />
                                {pendingId === p.id && (
                                    <div className="absolute inset-0 flex items-center justify-center bg-black/40 rounded-[--radius-md]">
                                        <LoadingSpinner className="m-0" />
                                    </div>
                                )}
                            </div>
                            <span className="flex items-center gap-1.5 text-[--muted] group-hover:text-white text-base md:text-lg max-w-full">
                                <span className="truncate">{p.name}</span>
                                {p.hasPin && <LuLock className="shrink-0 text-sm" aria-label="Protected with a PIN" />}
                            </span>
                        </button>
                    ))}
                    <button
                        type="button"
                        onClick={() => setAdding(true)}
                        data-add-profile
                        className="group flex flex-col items-center gap-3 w-28 md:w-36 focus-visible:outline-none"
                    >
                        <div className="w-full aspect-square rounded-[--radius-md] border-2 border-dashed border-[--border] flex items-center justify-center transition group-hover:scale-105 group-hover:border-white group-focus-visible:border-white">
                            <LuPlus className="text-4xl text-[--muted] group-hover:text-white" />
                        </div>
                        <span className="text-[--muted] group-hover:text-white text-base md:text-lg">Add profile</span>
                    </button>
                </div>
            )}

            {pinProfile && <PinDialog
                profile={pinProfile}
                onClose={() => setPinProfile(null)}
                onEnter={async (pin) => {
                    const session = await selectProfileRequest(pinProfile.id, password, pin)
                    if (!session) throw new Error()
                    await enter({ profileToken: session.profileToken, expiresAt: session.expiresAt, profileId: pinProfile.id })
                }}
            />}
        </div>
    )
}

function PinDialog({ profile, onClose, onEnter }: { profile: Profile, onClose: () => void, onEnter: (pin: string) => Promise<void> }) {
    const [error, setError] = React.useState("")
    const [busy, setBusy] = React.useState(false)
    const [resetKey, setResetKey] = React.useState(0)
    const [lockedUntil, setLockedUntil] = React.useState<number | null>(null)
    const [now, setNow] = React.useState(Date.now())

    // Lockout countdown (429 + Retry-After).
    React.useEffect(() => {
        if (!lockedUntil) return
        const t = setInterval(() => {
            setNow(Date.now())
            if (Date.now() >= lockedUntil) {
                setLockedUntil(null)
                setError("")
            }
        }, 1000)
        return () => clearInterval(t)
    }, [lockedUntil])

    const locked = !!lockedUntil && now < lockedUntil
    const remaining = locked ? Math.ceil((lockedUntil! - now) / 1000) : 0

    async function submit(pin: string) {
        setBusy(true)
        setError("")
        try {
            await onEnter(pin)
        }
        catch (err) {
            const code = getSeaErrorCode(err)
            if (code === "PROFILE_PIN_LOCKED") {
                const retryAfter = axios.isAxiosError(err) ? Number(err.response?.headers?.["retry-after"]) : NaN
                setLockedUntil(Date.now() + (Number.isFinite(retryAfter) && retryAfter > 0 ? retryAfter : 300) * 1000)
                setNow(Date.now())
            } else {
                setError(getProfileErrorMessage(code) || "Couldn't enter the profile")
            }
            setResetKey(k => k + 1)
        }
        finally {
            setBusy(false)
        }
    }

    return (
        <Modal
            open
            onOpenChange={v => !v && onClose()}
            title={<span className="flex items-center gap-3">
                <ProfileAvatar url={profile.avatarUrl} name={profile.name} color={profile.color} className="w-10" />
                {profile.name}
            </span>}
            contentClass="max-w-sm"
        >
            <div data-pin-dialog className="py-2">
                <PinPad
                    onSubmit={submit}
                    disabled={busy || locked}
                    resetKey={resetKey}
                    error={locked
                        ? `Too many wrong PINs. Try again in ${remaining >= 60 ? `${Math.ceil(remaining / 60)} min` : `${remaining} s`}.`
                        : error}
                />
            </div>
        </Modal>
    )
}
