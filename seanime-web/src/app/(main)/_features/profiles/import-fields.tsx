import { RadioGroup } from "@/components/ui/radio-group"
import { TextInput } from "@/components/ui/text-input"
import React from "react"

export type ImportChoice =
    | { kind: "none" }
    | { kind: "anilist", username: string }
    | { kind: "server-account" }

type ImportChoiceFieldsProps = {
    value: ImportChoice
    onChange: (v: ImportChoice) => void
    /** Offer "Import this server's AniList account list" (the one-time migration). */
    showServerAccount?: boolean
}

/** Optional AniList import when creating a profile (plan §3.5). */
export function ImportChoiceFields({ value, onChange, showServerAccount }: ImportChoiceFieldsProps) {
    const options = [
        { value: "none", label: "Start with an empty list" },
        { value: "anilist", label: "Import my AniList list" },
        ...(showServerAccount ? [{ value: "server-account", label: "Import this server's AniList account list" }] : []),
    ]

    return (
        <div className="space-y-3" data-profile-import-choice>
            <RadioGroup
                value={value.kind}
                onValueChange={v => {
                    if (v === "anilist") onChange({ kind: "anilist", username: value.kind === "anilist" ? value.username : "" })
                    else if (v === "server-account") onChange({ kind: "server-account" })
                    else onChange({ kind: "none" })
                }}
                options={options}
            />
            {value.kind === "anilist" && (
                <TextInput
                    label="AniList username"
                    help="Only public lists can be imported. No AniList login is needed."
                    value={value.username}
                    onValueChange={v => onChange({ kind: "anilist", username: v.trim() })}
                    placeholder="username"
                    data-profile-import-username
                />
            )}
            {value.kind === "server-account" && (
                <p className="text-sm text-[--muted]">
                    Copies the list of the AniList account this server was using before profiles, if there is one.
                </p>
            )}
        </div>
    )
}
