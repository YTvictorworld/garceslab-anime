// Garceslab Anime phase 3: favourites, custom list management and "recently
// watched" for the current profile (shown on the My lists page).

import { ProfileCustomList } from "@/api/generated/types"
import {
    useCreateMyCustomList,
    useDeleteMyCustomList,
    useGetMyCustomLists,
    useGetMyFavourites,
    useGetMyRecentlyWatched,
    useUpdateMyCustomList,
} from "@/api/hooks/profiles_library.hooks"
import { MediaCardLazyGrid } from "@/app/(main)/_features/media/_components/media-card-grid"
import { MediaEntryCard } from "@/app/(main)/_features/media/_components/media-entry-card"
import { useCurrentProfile } from "@/app/(main)/_features/profiles/profile-menu"
import { Button, IconButton } from "@/components/ui/button"
import { Modal } from "@/components/ui/modal"
import { TextInput } from "@/components/ui/text-input"
import React from "react"
import { LuArrowDown, LuArrowUp, LuCheck, LuListPlus, LuPencil, LuPlus, LuTrash2, LuX } from "react-icons/lu"

/** The current profile's anime favourites. Renders nothing when there are none. */
export function ProfileFavouritesSection() {
    const profile = useCurrentProfile()
    const { data } = useGetMyFavourites("ANIME", !!profile)
    const favourites = React.useMemo(() => (data ?? []).filter(f => !!f.media), [data])
    if (!profile || !favourites.length) return null
    return <div className="space-y-6" data-profile-favourites-section>
        <h2>Favourites <span className="text-[--muted] font-medium ml-3">{favourites.length}</span></h2>
        <MediaCardLazyGrid itemCount={favourites.length}>
            {favourites.map(f => <MediaEntryCard key={f.refId} media={f.media!} type="anime" showLibraryBadge />)}
        </MediaCardLazyGrid>
    </div>
}

/** What the current profile watched lately, newest first. */
export function ProfileRecentlyWatchedSection() {
    const profile = useCurrentProfile()
    const { data } = useGetMyRecentlyWatched(18, !!profile)
    const items = React.useMemo(() => (data ?? []).filter(r => !!r.media), [data])
    if (!profile || !items.length) return null
    return <div className="space-y-6" data-profile-recently-watched-section>
        <h2>Recently watched</h2>
        <MediaCardLazyGrid itemCount={items.length}>
            {items.map(r => <div key={r.mediaId} className="space-y-1">
                <MediaEntryCard media={r.media!} type="anime" showLibraryBadge />
                <p className="text-sm text-[--muted] text-center line-clamp-1">
                    Episode {r.episodeNumber} · {new Date(r.updatedAt * 1000).toLocaleDateString()}
                </p>
            </div>)}
        </MediaCardLazyGrid>
    </div>
}

/** "Manage lists" button + modal: create, rename, reorder and delete custom lists. */
export function ProfileCustomListsButton() {
    const profile = useCurrentProfile()
    const [open, setOpen] = React.useState(false)
    if (!profile) return null
    return <>
        <Button intent="gray-subtle" leftIcon={<LuListPlus />} className="flex-none" onClick={() => setOpen(true)}>
            Manage lists
        </Button>
        <Modal open={open} onOpenChange={setOpen} title="Custom lists" contentClass="max-w-xl">
            {open && <ProfileCustomListsManager />}
        </Modal>
    </>
}

function ProfileCustomListsManager() {
    const { data: lists, isLoading } = useGetMyCustomLists()
    const { mutate: create, isPending: creating } = useCreateMyCustomList()
    const [name, setName] = React.useState("")

    const add = () => {
        const n = name.trim()
        if (!n) return
        create({ name: n }, { onSuccess: () => setName("") })
    }

    return <div className="space-y-4" data-profile-custom-lists-manager>
        <p className="text-sm text-[--muted]">
            Add anime to a list from its editor on the anime page. Deleting a list keeps the anime in your list.
        </p>
        {!isLoading && !lists?.length && <p className="text-sm">No custom lists yet.</p>}
        <div className="space-y-2">
            {lists?.map((l, i) => <CustomListRow key={l.id} list={l} index={i} count={lists.length} />)}
        </div>
        <div className="flex gap-2 items-center">
            <TextInput
                placeholder="New list name"
                value={name}
                maxLength={50}
                onValueChange={setName}
                onKeyDown={e => {
                    if (e.key === "Enter") add()
                }}
            />
            <Button leftIcon={<LuPlus />} loading={creating} disabled={!name.trim()} onClick={add}>Create</Button>
        </div>
    </div>
}

function CustomListRow({ list, index, count }: { list: ProfileCustomList, index: number, count: number }) {
    const { mutate: update, isPending: updating } = useUpdateMyCustomList()
    const { mutate: remove, isPending: removing } = useDeleteMyCustomList()
    const [editing, setEditing] = React.useState(false)
    const [confirmDelete, setConfirmDelete] = React.useState(false)
    const [name, setName] = React.useState(list.name)

    const save = () => {
        const n = name.trim()
        if (!n || n === list.name) {
            setEditing(false)
            return
        }
        update({ id: list.id, name: n }, { onSuccess: () => setEditing(false) })
    }

    return <div className="flex items-center gap-2 border rounded-[--radius] px-3 py-2" data-profile-custom-list-row>
        {editing ? <TextInput
            value={name}
            maxLength={50}
            autoFocus
            onValueChange={setName}
            onKeyDown={e => {
                if (e.key === "Enter") save()
                if (e.key === "Escape") setEditing(false)
            }}
        /> : <p className="flex-1 font-medium line-clamp-1">
            {list.name} <span className="text-[--muted] ml-2">{list.mediaIds?.length ?? 0}</span>
        </p>}
        {editing ? <>
            <IconButton size="sm" intent="primary-subtle" icon={<LuCheck />} loading={updating} onClick={save} aria-label="Save" />
            <IconButton size="sm" intent="gray-subtle" icon={<LuX />} onClick={() => {
                setName(list.name)
                setEditing(false)
            }} aria-label="Cancel" />
        </> : <>
            <IconButton size="sm" intent="gray-subtle" icon={<LuArrowUp />} disabled={index === 0 || updating}
                        onClick={() => update({ id: list.id, position: index - 1 })} aria-label="Move up" />
            <IconButton size="sm" intent="gray-subtle" icon={<LuArrowDown />} disabled={index === count - 1 || updating}
                        onClick={() => update({ id: list.id, position: index + 1 })} aria-label="Move down" />
            <IconButton size="sm" intent="gray-subtle" icon={<LuPencil />} onClick={() => setEditing(true)} aria-label="Rename" />
            {confirmDelete
                ? <Button size="sm" intent="alert" loading={removing} onClick={() => remove({ id: list.id })}>Delete</Button>
                : <IconButton size="sm" intent="alert-subtle" icon={<LuTrash2 />} onClick={() => setConfirmDelete(true)} aria-label="Delete" />}
        </>}
    </div>
}
