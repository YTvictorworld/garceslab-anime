import { getProfileErrorMessage, getSeaErrorCode } from "@/api/client/requests"
import { API_ENDPOINTS } from "@/api/generated/endpoints"
import { Profile, ProfileAuditEntry } from "@/api/generated/types"
import {
    describeImportResult,
    exportProfileRequest,
    ImportMode,
    importAnilistWithOptionsRequest,
    importJSONRequest,
    importServerAccountRequest,
    selectProfileRequest,
    useCreateProfile,
    useGetAvatarCatalog,
    useGetProfileAudit,
    useGetProfiles,
    useGetViewerSessions,
    useRestoreProfile,
    useStopViewerSession,
    useSetProfilePin,
    useTrashProfile,
    useUpdateProfile,
} from "@/api/hooks/profiles.hooks"
import { serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { useCurrentProfile } from "@/app/(main)/_features/profiles/profile-menu"
import { isValidPin, PIN_NOTICE, PinFields, ProfileIdentityFields } from "@/app/(main)/_features/profiles/profile-form"
import { ProfileAvatar } from "@/app/(main)/_features/profiles/profile-avatar"
import { useProfileSwitcher } from "@/app/(main)/_features/profiles/use-profile-switcher"
import { SettingsCard } from "@/app/(main)/settings/_components/settings-card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { LoadingSpinner } from "@/components/ui/loading-spinner"
import { Modal } from "@/components/ui/modal"
import { RadioGroup } from "@/components/ui/radio-group"
import { Switch } from "@/components/ui/switch"
import { TextInput } from "@/components/ui/text-input"
import { useQueryClient } from "@tanstack/react-query"
import { useAtomValue } from "jotai"
import React from "react"
import { LuDownload, LuKeyRound, LuLock, LuPencil, LuPlus, LuTrash2, LuUndo2, LuUpload } from "react-icons/lu"
import { toast } from "sonner"

const TRASH_DAYS = 30

function formatDate(unixSeconds?: number) {
    if (!unixSeconds) return ""
    return new Date(unixSeconds * 1000).toLocaleString()
}

const SESSION_STATE: Record<string, string> = {
    preparing: "Starting",
    ready: "Starting",
    playing: "Watching",
}

/**
 * Phase 2: who is watching right now. Debrid streams played directly by a
 * device take one of the viewing slots each; a playback through the server
 * (web player, torrent stream, library file) takes one too.
 */
function NowWatching() {
    const { data } = useGetViewerSessions()
    const { mutate: stop, isPending } = useStopViewerSession()
    if (!data) return null
    const sessions = data.sessions ?? []
    const used = sessions.length + (data.relayed ? 1 : 0)
    return (
        <SettingsCard title="Now watching">
            <div data-now-watching className="space-y-2">
                <p className="text-sm text-[--muted]">{used} of {data.max} viewing slots in use.</p>
                {sessions.map(s => (
                    <div key={s.id} data-viewer-session={s.profileName} className="flex flex-wrap items-center gap-3 p-2 rounded-[--radius-md] border border-[--border]">
                        <div className="flex-1 min-w-[8rem]">
                            <p className="font-semibold">{s.profileName || "Guest"} <span className="text-[--muted] font-normal">· {s.device}</span></p>
                            <p className="text-sm text-[--muted] truncate">
                                {s.mediaTitle || `Anime ${s.mediaId}`}{s.episodeNumber > 0 ? ` · Episode ${s.episodeNumber}` : ""}
                                {" · "}{SESSION_STATE[s.state] ?? s.state}{s.disconnected ? " · reconnecting" : ""}
                            </p>
                        </div>
                        <Button size="sm" intent="alert-subtle" disabled={isPending} onClick={() => stop({ sessionId: s.id })}>Stop</Button>
                    </div>
                ))}
                {data.relayed && (
                    <div className="p-2 rounded-[--radius-md] border border-[--border]">
                        <p className="font-semibold">{data.relayed.profileName || "Guest"} <span className="text-[--muted] font-normal">· through the server</span></p>
                    </div>
                )}
                {used === 0 && <p className="text-sm text-[--muted]">Nobody is watching.</p>}
            </div>
        </SettingsCard>
    )
}

/** Settings → Profiles (plan §3.3, §3.9.3). No roles: any profile can manage any profile. */
export function ProfilesSettings() {
    const { data: profiles, isLoading } = useGetProfiles(true)
    const current = useCurrentProfile()

    const [editing, setEditing] = React.useState<Profile | "new" | null>(null)
    const [pinFor, setPinFor] = React.useState<Profile | null>(null)
    const [deleting, setDeleting] = React.useState<Profile | null>(null)
    const [importFor, setImportFor] = React.useState<Profile | null>(null)

    const active = profiles?.filter(p => !p.deletedAt) ?? []
    const trashed = profiles?.filter(p => !!p.deletedAt) ?? []

    if (isLoading) return <LoadingSpinner />

    return (
        <div data-profiles-settings className="space-y-4">
            <SettingsCard>
                <div className="space-y-1 text-sm text-[--muted]">
                    <p>Any profile can create, edit or delete any profile and change any setting.</p>
                    <p>{PIN_NOTICE}</p>
                </div>
            </SettingsCard>

            <NowWatching />

            <SettingsCard title="Profiles">
                <div className="space-y-2">
                    {active.map(p => (
                        <div key={p.id} data-profile-row={p.name} className="flex flex-wrap items-center gap-3 p-2 rounded-[--radius-md] border border-[--border]">
                            <ProfileAvatar url={p.avatarUrl} name={p.name} color={p.color} className="w-12" />
                            <div className="flex-1 min-w-[8rem]">
                                <p className="font-semibold flex items-center gap-2">
                                    <span className="truncate">{p.name}</span>
                                    {p.hasPin && <LuLock className="text-[--muted] text-sm" aria-label="Protected with a PIN" />}
                                    {current?.id === p.id && <Badge size="sm" intent="primary">You</Badge>}
                                </p>
                                {!!p.lastUsedAt && <p className="text-xs text-[--muted]">Last used {formatDate(p.lastUsedAt)}</p>}
                            </div>
                            <div className="flex flex-wrap gap-1">
                                <Button size="sm" intent="gray-subtle" leftIcon={<LuPencil />} onClick={() => setEditing(p)}>Edit</Button>
                                <Button size="sm" intent="gray-subtle" leftIcon={<LuKeyRound />} onClick={() => setPinFor(p)}>PIN</Button>
                                <Button size="sm" intent="gray-subtle" leftIcon={<LuUpload />} onClick={() => setImportFor(p)}>Import</Button>
                                <ExportButton profile={p} />
                                <Button size="sm" intent="alert-subtle" leftIcon={<LuTrash2 />} onClick={() => setDeleting(p)}>Delete</Button>
                            </div>
                        </div>
                    ))}
                </div>
                <Button intent="primary-subtle" size="sm" leftIcon={<LuPlus />} onClick={() => setEditing("new")}>Add profile</Button>
            </SettingsCard>

            {trashed.length > 0 && <SettingsCard
                title="Trash"
                description={`Deleted profiles can be restored for ${TRASH_DAYS} days. Then they are purged with all their data.`}
            >
                <div className="space-y-2">
                    {trashed.map(p => <TrashRow key={p.id} profile={p} />)}
                </div>
            </SettingsCard>}

            <AuditCard profiles={profiles ?? []} />

            {editing && <EditProfileModal profile={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}
            {pinFor && <PinModal profile={pinFor} isCurrent={current?.id === pinFor.id} onClose={() => setPinFor(null)} />}
            {deleting && <DeleteProfileModal profile={deleting} isCurrent={current?.id === deleting.id} onClose={() => setDeleting(null)} />}
            {importFor && <ImportModal profile={importFor} onClose={() => setImportFor(null)} />}
        </div>
    )
}

function TrashRow({ profile }: { profile: Profile }) {
    const { mutate: restore, isPending } = useRestoreProfile()
    const purgeAt = (profile.deletedAt ?? 0) + TRASH_DAYS * 86400
    return (
        <div data-trash-row={profile.name} className="flex items-center gap-3 p-2 rounded-[--radius-md] border border-dashed border-[--border]">
            <ProfileAvatar url={profile.avatarUrl} name={profile.name} className="w-10 opacity-60" />
            <div className="flex-1">
                <p className="font-semibold">{profile.name}</p>
                <p className="text-xs text-[--muted]">Deleted {formatDate(profile.deletedAt)} · purged after {formatDate(purgeAt)}</p>
            </div>
            <Button size="sm" intent="gray-subtle" leftIcon={<LuUndo2 />} loading={isPending} onClick={() => restore({ id: profile.id })}>
                Restore
            </Button>
        </div>
    )
}

const AUDIT_SENTENCES: Record<string, (actor: string, target: string) => string> = {
    create: (a, t) => `${a} created ${t}`,
    update: (a, t) => `${a} edited ${t}`,
    rename: (a, t) => `${a} renamed ${t}`,
    delete: (a, t) => `${a} moved ${t} to the trash`,
    restore: (a, t) => `${a} restored ${t}`,
    purge: (_, t) => `${t} was purged from the trash`,
    pin_set: (a, t) => `${a} set the PIN of ${t}`,
    pin_change: (a, t) => `${a} changed the PIN of ${t}`,
    pin_remove: (a, t) => `${a} removed the PIN of ${t}`,
    import: (a, t) => `${a} imported a list into ${t}`,
    export: (a, t) => `${a} exported ${t}`,
}

function AuditCard({ profiles }: { profiles: Profile[] }) {
    const { data: audit } = useGetProfileAudit()
    const byId = React.useMemo(() => new Map(profiles.map(p => [p.id, p])), [profiles])

    function sentence(e: ProfileAuditEntry) {
        const actor = e.actorProfileId ? (byId.get(e.actorProfileId)?.name ?? "A deleted profile") : "Someone (no profile)"
        const self = e.actorProfileId === e.targetProfileId && e.action !== "create"
        const target = self ? "their profile" : e.targetName
        return (AUDIT_SENTENCES[e.action] ?? ((a, t) => `${a}: ${e.action} ${t}`))(actor, target)
    }

    return (
        <SettingsCard title="Recent changes" description="Who changed which profile, and when. Kept after a profile is purged.">
            {!audit?.length ? <p className="text-sm text-[--muted]">Nothing yet.</p> : (
                <ul data-profile-audit className="space-y-2 text-sm max-h-80 overflow-y-auto">
                    {audit.map(e => {
                        const actor = e.actorProfileId ? byId.get(e.actorProfileId) : undefined
                        return (
                            <li key={e.id} data-audit-action={e.action} className="flex items-center gap-3">
                                <ProfileAvatar url={actor?.avatarUrl} name={actor?.name} className="w-7" />
                                <div className="flex-1 min-w-0">
                                    <p className="truncate">{sentence(e)}</p>
                                    {e.detail && <p className="text-xs text-[--muted] truncate">{e.detail}</p>}
                                </div>
                                <span className="text-xs text-[--muted] shrink-0">{formatDate(e.createdAt)}</span>
                            </li>
                        )
                    })}
                </ul>
            )}
        </SettingsCard>
    )
}

function EditProfileModal({ profile, onClose }: { profile: Profile | null, onClose: () => void }) {
    const { data: catalog } = useGetAvatarCatalog()
    const { mutate: update, isPending: updating } = useUpdateProfile(profile?.id ?? 0)
    const { mutate: create, isPending: creating } = useCreateProfile()

    const [name, setName] = React.useState(profile?.name ?? "")
    const [avatarId, setAvatarId] = React.useState(profile?.avatarId ?? "")
    const [color, setColor] = React.useState(profile?.color ?? "")
    const [pin, setPin] = React.useState("")
    const [pinConfirm, setPinConfirm] = React.useState("")

    React.useEffect(() => {
        if (!avatarId && catalog?.length) setAvatarId(catalog[0].id)
    }, [catalog, avatarId])

    const pinOk = !!profile || pin.length === 0 || (isValidPin(pin) && pin === pinConfirm)
    const canSave = name.trim().length > 0 && !!avatarId && pinOk

    function save() {
        if (!canSave) return
        if (profile) {
            update({ name: name.trim(), avatarId, color }, { onSuccess: onClose })
        } else {
            create({ name: name.trim(), avatarId, color: color || undefined, pin: pin || undefined }, {
                onSuccess: (p) => {
                    toast.success(`Profile "${p?.name}" created`)
                    onClose()
                },
            })
        }
    }

    return (
        <Modal open onOpenChange={v => !v && onClose()} title={profile ? `Edit ${profile.name}` : "Add profile"} contentClass="max-w-3xl">
            <div className="space-y-5">
                <ProfileIdentityFields
                    name={name}
                    onNameChange={setName}
                    avatarId={avatarId}
                    onAvatarChange={a => setAvatarId(a.id)}
                    color={color}
                    onColorChange={setColor}
                    autoFocus
                />
                {!profile && <div className="space-y-2">
                    <p className="font-semibold text-sm">PIN <span className="font-normal text-[--muted]">(optional)</span></p>
                    <PinFields pin={pin} confirm={pinConfirm} onPinChange={setPin} onConfirmChange={setPinConfirm} />
                </div>}
                <div className="flex justify-end gap-2">
                    <Button intent="gray-outline" onClick={onClose}>Cancel</Button>
                    <Button intent="primary" onClick={save} disabled={!canSave} loading={updating || creating} data-save-profile>Save</Button>
                </div>
            </div>
        </Modal>
    )
}

/** Typing the name confirms destructive changes to a profile (plan §3.3). */
function NameConfirmation({ name, value, onChange }: { name: string, value: string, onChange: (v: string) => void }) {
    return (
        <TextInput
            label={<>Type <span className="font-bold">{name}</span> to confirm</>}
            value={value}
            onValueChange={onChange}
            autoComplete="off"
            data-confirm-name-input
        />
    )
}

function PinModal({ profile, isCurrent, onClose }: { profile: Profile, isCurrent: boolean, onClose: () => void }) {
    const password = useAtomValue(serverAuthTokenAtom)
    const { mutateAsync: setPinMutation, isPending } = useSetProfilePin()
    const { enter } = useProfileSwitcher()

    const [pin, setPin] = React.useState("")
    const [confirm, setConfirm] = React.useState("")
    const [confirmName, setConfirmName] = React.useState("")

    // Changing another profile's PIN asks for its name.
    const nameOk = isCurrent || confirmName.trim() === profile.name
    const pinOk = isValidPin(pin) && pin === confirm

    async function apply(newPin: string | null) {
        try {
            await setPinMutation({ id: profile.id, pin: newPin })
        }
        catch {
            return
        }
        toast.success(newPin ? "PIN saved" : "PIN removed")
        // Any PIN change closes the profile's sessions, ours included: enter again.
        if (isCurrent) {
            try {
                const session = await selectProfileRequest(profile.id, password, newPin ?? undefined)
                if (session) await enter({ profileToken: session.profileToken, expiresAt: session.expiresAt, profileId: profile.id })
            }
            catch {
            }
        }
        onClose()
    }

    return (
        <Modal open onOpenChange={v => !v && onClose()} title={`PIN of ${profile.name}`}>
            <div className="space-y-4">
                <PinFields pin={pin} confirm={confirm} onPinChange={setPin} onConfirmChange={setConfirm} />
                {!isCurrent && <NameConfirmation name={profile.name} value={confirmName} onChange={setConfirmName} />}
                <div className="flex flex-wrap justify-end gap-2">
                    {profile.hasPin && <Button intent="alert-subtle" disabled={!nameOk} loading={isPending} onClick={() => apply(null)}>
                        Remove PIN
                    </Button>}
                    <Button intent="primary" disabled={!nameOk || !pinOk} loading={isPending} onClick={() => apply(pin)} data-save-pin>
                        {profile.hasPin ? "Change PIN" : "Set PIN"}
                    </Button>
                </div>
            </div>
        </Modal>
    )
}

function DeleteProfileModal({ profile, isCurrent, onClose }: { profile: Profile, isCurrent: boolean, onClose: () => void }) {
    const { mutateAsync: trash, isPending } = useTrashProfile()
    const { leave } = useProfileSwitcher()
    const [confirmName, setConfirmName] = React.useState("")

    async function del() {
        try {
            await trash({ id: profile.id })
        }
        catch {
            return
        }
        onClose()
        if (isCurrent) await leave()
    }

    return (
        <Modal open onOpenChange={v => !v && onClose()} title={`Delete ${profile.name}?`}>
            <div className="space-y-4">
                <p className="text-sm text-[--muted]">
                    The profile goes to the trash with its list, history and settings. It can be restored for {TRASH_DAYS} days.
                    {isCurrent && " This is your current profile: you'll go back to \"Who's watching?\"."}
                </p>
                <NameConfirmation name={profile.name} value={confirmName} onChange={setConfirmName} />
                <div className="flex justify-end gap-2">
                    <Button intent="gray-outline" onClick={onClose}>Cancel</Button>
                    <Button intent="alert" disabled={confirmName.trim() !== profile.name} loading={isPending} onClick={del} data-confirm-delete>
                        Delete
                    </Button>
                </div>
            </div>
        </Modal>
    )
}

function ExportButton({ profile }: { profile: Profile }) {
    const password = useAtomValue(serverAuthTokenAtom)
    const qc = useQueryClient()
    const [busy, setBusy] = React.useState(false)

    async function download() {
        setBusy(true)
        try {
            const data = await exportProfileRequest(profile.id, password)
            const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" })
            const url = URL.createObjectURL(blob)
            const a = document.createElement("a")
            a.href = url
            a.download = `garceslab-anime-profile-${profile.name.replace(/[^\w.-]+/g, "_")}.json`
            document.body.appendChild(a)
            a.click()
            a.remove()
            setTimeout(() => URL.revokeObjectURL(url), 1000)
            void qc.invalidateQueries({ queryKey: [API_ENDPOINTS.PROFILES.GetProfileAudit.key] })
        }
        catch (err) {
            toast.error(getProfileErrorMessage(getSeaErrorCode(err)) || "The export failed")
        }
        finally {
            setBusy(false)
        }
    }

    return <Button size="sm" intent="gray-subtle" leftIcon={<LuDownload />} loading={busy} onClick={download}>Export</Button>
}

function ImportModal({ profile, onClose }: { profile: Profile, onClose: () => void }) {
    const password = useAtomValue(serverAuthTokenAtom)
    const qc = useQueryClient()
    const current = useCurrentProfile()

    const [source, setSource] = React.useState<"anilist" | "anilist-token" | "json" | "server-account">("anilist")
    const [mode, setMode] = React.useState<ImportMode>("merge")
    const [username, setUsername] = React.useState("")
    // Used for this one import only: never saved by the browser or the server.
    const [token, setToken] = React.useState("")
    const [clientId, setClientId] = React.useState("")
    const [favourites, setFavourites] = React.useState(true)
    const [file, setFile] = React.useState<File | null>(null)
    const [busy, setBusy] = React.useState(false)

    const canImport = !busy && (source === "anilist" ? username.trim().length > 0
        : source === "anilist-token" ? token.trim().length > 0
            : source === "json" ? !!file : true)

    async function run() {
        if (!canImport) return
        setBusy(true)
        try {
            let res
            if (source === "anilist") {
                res = await importAnilistWithOptionsRequest(profile.id, { username: username.trim(), mode, favourites }, password)
            } else if (source === "anilist-token") {
                res = await importAnilistWithOptionsRequest(profile.id, { token: token.trim(), mode, favourites }, password)
                setToken("")
            } else if (source === "server-account") {
                res = await importServerAccountRequest(profile.id, mode, password)
            } else {
                let data: unknown
                try {
                    data = JSON.parse(await file!.text())
                }
                catch {
                    toast.error("This file isn't valid JSON.")
                    return
                }
                res = await importJSONRequest(profile.id, data, mode, password)
            }
            toast.success(describeImportResult(res))
            if (res?.errors?.length) toast.warning(`${res.errors.length} entries couldn't be imported`)
            // The current profile's lists changed: refetch everything.
            if (current?.id === profile.id) await qc.invalidateQueries()
            else void qc.invalidateQueries({ queryKey: [API_ENDPOINTS.PROFILES.GetProfileAudit.key] })
            onClose()
        }
        catch (err) {
            toast.error(getProfileErrorMessage(getSeaErrorCode(err)) || "The import failed")
        }
        finally {
            setBusy(false)
        }
    }

    return (
        <Modal open onOpenChange={v => !v && onClose()} title={`Import into ${profile.name}`}>
            <div className="space-y-4" data-import-modal>
                <RadioGroup
                    value={source}
                    onValueChange={v => setSource(v as typeof source)}
                    options={[
                        { value: "anilist", label: "Public AniList list (username)" },
                        { value: "anilist-token", label: "Private AniList list (access token)" },
                        { value: "json", label: "Garceslab Anime export (JSON file)" },
                        { value: "server-account", label: "This server's AniList account list" },
                    ]}
                />
                {source === "anilist" && <TextInput
                    label="AniList username"
                    help="Only public lists can be imported. No AniList login is needed."
                    value={username}
                    onValueChange={setUsername}
                />}
                {source === "anilist-token" && <div className="space-y-3" data-import-token>
                    <p className="text-sm text-[--muted]">
                        The token is sent once for this import and is never saved. To get one, create an API client
                        on AniList (Settings → Developer) with the redirect URL <code>https://anilist.co/api/v2/oauth/pin</code>,
                        then open the authorization link and copy the token AniList shows you.
                    </p>
                    <div className="flex gap-2 items-end">
                        <TextInput
                            label="AniList API client ID"
                            help="Only used to build the link below."
                            value={clientId}
                            inputMode="numeric"
                            onValueChange={v => setClientId(v.replace(/\D/g, ""))}
                        />
                        <Button
                            intent="gray-subtle"
                            disabled={!clientId}
                            onClick={() => window.open(`https://anilist.co/api/v2/oauth/authorize?client_id=${clientId}&response_type=token`,
                                "_blank", "noopener,noreferrer")}
                        >Open AniList</Button>
                    </div>
                    <TextInput
                        label="AniList access token"
                        type="password"
                        autoComplete="off"
                        value={token}
                        onValueChange={setToken}
                    />
                </div>}
                {(source === "anilist" || source === "anilist-token") && <Switch
                    label="Also import favourites"
                    value={favourites}
                    onValueChange={setFavourites}
                />}
                {source === "json" && <input
                    type="file"
                    accept="application/json,.json"
                    onChange={e => setFile(e.target.files?.[0] ?? null)}
                    className="block text-sm"
                    data-import-file
                />}
                <div className="space-y-1">
                    <p className="text-sm font-semibold">Mode</p>
                    <RadioGroup
                        value={mode}
                        onValueChange={v => setMode(v as ImportMode)}
                        options={[
                            { value: "merge", label: "Merge with the current list" },
                            { value: "replace", label: "Replace the current list" },
                        ]}
                    />
                </div>
                <div className="flex justify-end gap-2">
                    <Button intent="gray-outline" onClick={onClose}>Cancel</Button>
                    <Button intent="primary" disabled={!canImport} loading={busy} onClick={run}>Import</Button>
                </div>
            </div>
        </Modal>
    )
}
