import { useCreateMyCustomList, useGetMyCustomLists } from "@/api/hooks/profiles_library.hooks"
import { Button } from "@/components/ui/button"
import { CheckboxGroup } from "@/components/ui/checkbox"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { TextInput } from "@/components/ui/text-input"
import React from "react"
import { LuPlus } from "react-icons/lu"

/** Entry fields the upstream editor doesn't have (Garceslab Anime profiles, phase 3). */
export type ProfileEntryExtrasState = {
    private: boolean
    notes: string
    customListIds: number[]
}

export const EMPTY_ENTRY_EXTRAS: ProfileEntryExtrasState = { private: false, notes: "", customListIds: [] }

export function ProfileEntryExtras({ value, onChange }: {
    value: ProfileEntryExtrasState
    onChange: (v: ProfileEntryExtrasState) => void
}) {
    const { data: lists } = useGetMyCustomLists()
    const { mutate: createList, isPending: creating } = useCreateMyCustomList()
    const [newName, setNewName] = React.useState("")

    const addList = () => {
        const name = newName.trim()
        if (!name) return
        createList({ name }, {
            onSuccess: res => {
                setNewName("")
                const created = res?.find(l => l.name.toLowerCase() === name.toLowerCase())
                if (created && !value.customListIds.includes(created.id)) {
                    onChange({ ...value, customListIds: [...value.customListIds, created.id] })
                }
            },
        })
    }

    return <div className="space-y-4 mt-4" data-profile-entry-extras>
        <Textarea
            label="Notes"
            value={value.notes}
            maxLength={5000}
            onValueChange={notes => onChange({ ...value, notes })}
        />
        <Switch
            label="Private"
            help="Like AniList's private flag. Nothing on this server is public; it is kept in imports and exports."
            value={value.private}
            onValueChange={v => onChange({ ...value, private: v })}
        />
        <div className="space-y-2">
            <p className="font-semibold text-sm">Custom lists</p>
            {!!lists?.length ? <CheckboxGroup
                options={lists.map(l => ({ value: String(l.id), label: l.name }))}
                value={value.customListIds.map(String)}
                onValueChange={ids => onChange({ ...value, customListIds: ids.map(Number) })}
                stackClass="flex flex-wrap gap-x-4 gap-y-2"
            /> : <p className="text-sm text-[--muted]">No custom lists yet.</p>}
            <div className="flex gap-2 items-center">
                <TextInput
                    placeholder="New list name"
                    value={newName}
                    maxLength={50}
                    onValueChange={setNewName}
                    onKeyDown={e => {
                        if (e.key === "Enter") {
                            e.preventDefault()
                            addList()
                        }
                    }}
                />
                <Button intent="gray-subtle" size="md" leftIcon={<LuPlus />} loading={creating} disabled={!newName.trim()} onClick={addList}>
                    Create
                </Button>
            </div>
        </div>
    </div>
}
