import {
    useDismissTvUpdate,
    useGetTvUpdatesSettings,
    useGetTvUpdatesStatus,
    useInstallTvUpdate,
    useSaveTvUpdatesSettings,
} from "@/api/hooks/tv_updates.hooks"
import { SettingsCard } from "@/app/(main)/settings/_components/settings-card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { TextInput } from "@/components/ui/text-input"
import {
    describeBuild,
    describeInstallPhase,
    describeLastResult,
    formatBuildDate,
    groupChanges,
    TvUpdaterChange,
    TvUpdaterStatus,
} from "@/lib/tv-updates/tv-updates"
import React from "react"

/**
 * Garceslab Anime: Settings → TV updates (docs/tv-updates.md). The updater's
 * URL and token are saved on the server; the token is never shown again. The
 * status card appears only when an updater is configured.
 */
export function TvUpdatesSettings() {
    const { data: settings } = useGetTvUpdatesSettings()
    return (
        <div data-tv-updates-settings className="space-y-4">
            {settings?.configured && <TvUpdateStatusCard />}
            <TvUpdaterSettingsCard />
        </div>
    )
}

function TvUpdaterSettingsCard() {
    const { data: settings } = useGetTvUpdatesSettings()
    const { mutate: save, isPending } = useSaveTvUpdatesSettings()
    const [url, setUrl] = React.useState("")
    const [token, setToken] = React.useState("")

    React.useEffect(() => {
        if (settings) setUrl(settings.url ?? "")
    }, [settings?.url])

    if (!settings) return null

    const submit = () => {
        const patch: { url?: string, token?: string } = {}
        if (!settings.urlFromEnv && url.trim() !== settings.url) patch.url = url.trim()
        if (!settings.tokenFromEnv && token.trim() !== "") patch.token = token.trim()
        if (Object.keys(patch).length === 0) return
        save(patch, { onSuccess: () => setToken("") })
    }

    return (
        <SettingsCard
            title="TV updater"
            description="Optional. A service on your server that installs new versions of the TV app, only after you confirm."
        >
            <div className="space-y-3" data-tv-updater-form>
                <TextInput
                    label="Updater URL"
                    placeholder="http://tv-updater:8095"
                    value={url}
                    onValueChange={setUrl}
                    disabled={settings.urlFromEnv}
                    help={settings.urlFromEnv ? "Set by SEANIME_TV_UPDATER_URL on the server." : "Leave empty to turn TV updates off."}
                />
                <TextInput
                    label="Token"
                    type="password"
                    autoComplete="off"
                    placeholder={settings.tokenConfigured ? "A token is saved (hidden)" : "The updater's UPDATER_TOKEN"}
                    value={token}
                    onValueChange={setToken}
                    disabled={settings.tokenFromEnv}
                    help={settings.tokenFromEnv
                        ? "Set by SEANIME_TV_UPDATER_TOKEN on the server."
                        : "Stored on the server only and never shown again. Leave empty to keep the saved one."}
                />
                <div className="flex flex-wrap gap-2">
                    <Button intent="primary" size="sm" loading={isPending} onClick={submit}
                            disabled={settings.urlFromEnv && settings.tokenFromEnv}>Save</Button>
                    {settings.tokenConfigured && !settings.tokenFromEnv && (
                        <Button intent="alert-subtle" size="sm" disabled={isPending} onClick={() => save({ token: "" })}>Remove token</Button>
                    )}
                </div>
            </div>
        </SettingsCard>
    )
}

function ChangeList({ title, changes }: { title: string, changes: TvUpdaterChange[] }) {
    if (!changes.length) return null
    return (
        <div>
            <p className="font-semibold text-sm">{title}</p>
            <ul className="list-disc pl-5 text-sm text-[--muted]">
                {changes.map((c, i) => (
                    <li key={`${c.pr ?? ""}-${i}`}>
                        {c.title}{c.pr ? <> (<a href={c.url} target="_blank" rel="noreferrer" className="underline">#{c.pr}</a>)</> : null}
                    </li>
                ))}
            </ul>
        </div>
    )
}

function TvUpdateStatusCard() {
    const { data, isLoading, refetch, isFetching } = useGetTvUpdatesStatus()
    const { mutate: install, isPending: installing } = useInstallTvUpdate()
    const { mutate: later, isPending: dismissing } = useDismissTvUpdate()

    if (isLoading || !data?.configured) return null

    if (!data.reachable) {
        return (
            <SettingsCard title="TV app update">
                <div data-tv-update-status="unreachable" className="space-y-2">
                    <p className="text-sm text-[--muted]">The TV updater is not answering{data.error ? `: ${data.error}` : "."}</p>
                    <Button size="sm" intent="gray-subtle" loading={isFetching} onClick={() => refetch()}>Try again</Button>
                </div>
            </SettingsCard>
        )
    }

    const s = (data.status ?? {}) as TvUpdaterStatus
    const pending = s.pending
    const grouped = groupChanges(pending?.changes)
    const phase = describeInstallPhase(s)
    const last = describeLastResult(s.lastResult)
    const busy = installing || dismissing

    return (
        <SettingsCard title="TV app update">
            <div data-tv-update-status={pending ? "pending" : "up-to-date"} className="space-y-3">
                <div className="flex flex-wrap gap-2 items-center text-sm">
                    {s.channel && <Badge size="sm" intent="gray">{s.channel}</Badge>}
                    <Badge size="sm" intent={s.tvOnline ? "success" : "gray"}>{s.tvOnline ? "TV on" : "TV off"}</Badge>
                    <span className="text-[--muted]">Installed: {describeBuild(s.installed)}</span>
                </div>

                {pending ? (
                    <div className="space-y-2 p-3 rounded-[--radius-md] border border-[--border]">
                        <p className="font-semibold">New version: {describeBuild(pending)}</p>
                        {formatBuildDate(pending.builtAt) && <p className="text-xs text-[--muted]">Built {formatBuildDate(pending.builtAt)}</p>}
                        <ChangeList title="New" changes={grouped.features} />
                        <ChangeList title="Fixes" changes={grouped.fixes} />
                        <ChangeList title="Other" changes={grouped.other} />
                        {pending.changesComplete === false && <p className="text-xs text-[--muted]">There may be more changes than listed.</p>}
                        {phase ? (
                            <div className="flex flex-wrap gap-2 items-center">
                                <p className="text-sm" data-tv-update-phase>{phase}…</p>
                                {s.installPhase === "waiting-tv" && (
                                    <Button intent="gray-subtle" size="sm" loading={dismissing} disabled={busy} onClick={() => later()}>Cancel</Button>
                                )}
                            </div>
                        ) : (
                            <>
                                <p className="text-xs text-[--muted]">Installing closes the TV app; it reopens if it was open.</p>
                                <div className="flex flex-wrap gap-2">
                                    <Button intent="primary" size="sm" loading={installing} disabled={busy} onClick={() => install()}>Install on TV</Button>
                                    <Button intent="gray-subtle" size="sm" loading={dismissing} disabled={busy} onClick={() => later()}>Later</Button>
                                </div>
                            </>
                        )}
                    </div>
                ) : (
                    <p className="text-sm text-[--muted]">{phase || "The TV app is up to date."}</p>
                )}

                {last && <p className="text-xs text-[--muted]" data-tv-update-last-result>Last result: {last}</p>}
            </div>
        </SettingsCard>
    )
}
