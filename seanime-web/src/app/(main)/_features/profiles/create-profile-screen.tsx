import { getProfileErrorMessage, getSeaErrorCode } from "@/api/client/requests"
import {
    describeImportResult,
    importAnilistRequest,
    importServerAccountRequest,
    selectProfileRequest,
    useCreateProfile,
    useGetAvatarCatalog,
} from "@/api/hooks/profiles.hooks"
import { serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { ImportChoice, ImportChoiceFields } from "@/app/(main)/_features/profiles/import-fields"
import { isValidPin, PinFields, ProfileIdentityFields } from "@/app/(main)/_features/profiles/profile-form"
import { useProfileSwitcher } from "@/app/(main)/_features/profiles/use-profile-switcher"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"
import { Switch } from "@/components/ui/switch"
import { useAtomValue } from "jotai"
import React from "react"
import { toast } from "sonner"

type CreateProfileScreenProps = {
    /** First run (no profiles yet): the new profile is entered right away. */
    firstRun?: boolean
    onCancel?: () => void
    /** Called after creating a profile from the picker (back to "Who's watching?"). */
    onCreated?: () => void
}

/** "Create your profile" (first run) and "Add profile" (plan §3.9.2). */
export function CreateProfileScreen({ firstRun, onCancel, onCreated }: CreateProfileScreenProps) {
    const password = useAtomValue(serverAuthTokenAtom)
    const { data: catalog } = useGetAvatarCatalog()
    const { mutateAsync: createProfile } = useCreateProfile(!!firstRun)
    const { enter, fetchStatus, applyStatus } = useProfileSwitcher()

    const [name, setName] = React.useState("")
    const [avatarId, setAvatarId] = React.useState("")
    const [color, setColor] = React.useState("")
    const [withPin, setWithPin] = React.useState(false)
    const [pin, setPin] = React.useState("")
    const [pinConfirm, setPinConfirm] = React.useState("")
    const [importChoice, setImportChoice] = React.useState<ImportChoice>({ kind: "none" })
    const [busy, setBusy] = React.useState(false)

    // Default to the first avatar of the catalog.
    React.useEffect(() => {
        if (!avatarId && catalog?.length) setAvatarId(catalog[0].id)
    }, [catalog, avatarId])

    const nameOk = name.trim().length > 0
    const pinOk = !withPin || (isValidPin(pin) && pin === pinConfirm)
    const importOk = importChoice.kind !== "anilist" || importChoice.username.length > 0
    const canSubmit = nameOk && !!avatarId && pinOk && importOk && !busy

    async function handleSubmit(e?: React.FormEvent) {
        e?.preventDefault()
        if (!canSubmit) return
        setBusy(true)
        try {
            const profile = await createProfile({
                name: name.trim(),
                avatarId,
                color: color || undefined,
                pin: withPin ? pin : undefined,
            })
            if (!profile) throw new Error("No profile returned")

            const needsSession = firstRun || importChoice.kind !== "none"
            if (!needsSession) {
                toast.success(`Profile "${profile.name}" created`)
                onCreated?.()
                return
            }

            const session = await selectProfileRequest(profile.id, password, withPin ? pin : undefined)
            if (!session) throw new Error("No session returned")

            if (importChoice.kind !== "none") {
                try {
                    const res = importChoice.kind === "anilist"
                        ? await importAnilistRequest(profile.id, importChoice.username, "merge", password, session.profileToken)
                        : await importServerAccountRequest(profile.id, "merge", password, session.profileToken)
                    toast.success(describeImportResult(res))
                }
                catch (err) {
                    // The profile exists anyway; the import can be retried from Settings → Profiles.
                    toast.error((getProfileErrorMessage(getSeaErrorCode(err)) || "The import failed") + " You can import later in Settings → Profiles.")
                }
            }

            if (firstRun) {
                await enter({ profileToken: session.profileToken, expiresAt: session.expiresAt, profileId: profile.id })
            } else {
                toast.success(`Profile "${profile.name}" created`)
                onCreated?.()
            }
        }
        catch (err) {
            // useServerMutation already shows a toast for API errors.
            if (!getSeaErrorCode(err)) toast.error(err instanceof Error ? err.message : "Couldn't create the profile")
            // If the profile was created but couldn't be entered, show the picker instead of this form.
            if (firstRun) applyStatus(await fetchStatus("").catch(() => undefined))
        }
        finally {
            setBusy(false)
        }
    }

    return (
        <div data-create-profile-screen className="container max-w-3xl py-10 px-4">
            <div className="mb-6 flex flex-col items-center gap-3 text-center">
                <img src="/garceslab-logo.png" alt="logo" className="w-14 h-auto" />
                <h2>{firstRun ? "Create your profile" : "Add profile"}</h2>
                <p className="text-[--muted]">
                    {firstRun
                        ? "Everyone in the house gets their own list, history and settings. You can add more profiles later."
                        : "A new profile with its own list, history and settings."}
                </p>
            </div>
            <Card className="p-4 md:p-6">
                <form className="space-y-6" onSubmit={handleSubmit}>
                    <ProfileIdentityFields
                        name={name}
                        onNameChange={setName}
                        avatarId={avatarId}
                        onAvatarChange={a => setAvatarId(a.id)}
                        color={color}
                        onColorChange={setColor}
                        autoFocus
                    />

                    <Separator />

                    <div className="space-y-3">
                        <Switch
                            side="right"
                            label="Protect with a PIN"
                            help="Optional. You can add or remove it later."
                            value={withPin}
                            onValueChange={setWithPin}
                        />
                        {withPin && (
                            <PinFields pin={pin} confirm={pinConfirm} onPinChange={setPin} onConfirmChange={setPinConfirm} />
                        )}
                    </div>

                    <Separator />

                    <div className="space-y-2">
                        <p className="font-semibold">Your list <span className="font-normal text-[--muted]">(optional)</span></p>
                        <ImportChoiceFields value={importChoice} onChange={setImportChoice} showServerAccount={firstRun} />
                    </div>

                    <div className="flex gap-2 justify-end">
                        {onCancel && <Button type="button" intent="gray-outline" onClick={onCancel} disabled={busy}>Cancel</Button>}
                        <Button type="submit" intent="primary" loading={busy} disabled={!canSubmit} data-create-profile-submit>
                            {firstRun ? "Create and continue" : "Create profile"}
                        </Button>
                    </div>
                </form>
            </Card>
        </div>
    )
}
