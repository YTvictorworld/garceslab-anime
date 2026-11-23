import { getServerBaseUrl } from "@/api/client/server-url"
import { SERVER_AUTH_TOKEN_STORAGE_KEY, serverAuthTokenAtom } from "@/app/(main)/_atoms/server-status.atoms"
import { getClientId, getClientIdProof, setClientIdentity } from "@/lib/server/client-id"
import { clearProfileSession, getProfileToken, isProfileErrorCode } from "@/lib/server/profile-session"
import { __clientPlatform__ } from "@/types/constants"
import { useMutation, UseMutationOptions, useQuery, UseQueryOptions } from "@tanstack/react-query"
import axios, { AxiosError } from "axios"
import { useAtomValue } from "jotai"
import { useEffect } from "react"
import { toast } from "sonner"

type SeaError = AxiosError<{ error: string }>

let authRedirectInProgress = false

type SeaQuery<D> = {
    endpoint: string
    method: "POST" | "GET" | "PATCH" | "DELETE" | "PUT"
    data?: D
    params?: D
    password?: string
    /** Overrides the tab's profile token (e.g. importing into a profile just created). */
    profileToken?: string
    signal?: AbortSignal
}

export function useSeaQuery() {
    const password = useAtomValue(serverAuthTokenAtom)

    return {
        seaFetch: <T, D extends any = any>(endpoint: string, method: "POST" | "GET" | "PATCH" | "DELETE" | "PUT", data?: D, params?: D) => {
            return buildSeaQuery<T, D>({
                endpoint,
                method,
                data,
                params,
                password,
            })
        },
    }
}

function getSeaErrorMessage(data: unknown): string {
    if (typeof data === "string") {
        return data.trim()
    }

    if (typeof data === "object" && data !== null && "error" in data && typeof data.error === "string") {
        return data.error.trim()
    }

    return ""
}

function isAuthFailureResponse(status: number | undefined, data: unknown): boolean {
    // PROFILE_* errors are about the profile, never about the server password.
    if (isProfileErrorCode(getSeaErrorMessage(data))) return false

    const errorMessage = getSeaErrorMessage(data).toLowerCase()

    if (status === 401) {
        return errorMessage === "" || errorMessage.includes("unauthenticated") || errorMessage.includes("unauthorized")
    }

    return status === 429 && errorMessage.includes("too many authentication attempts")
}

function clearStoredServerAuthToken() {
    if (typeof window === "undefined") return

    try {
        window.localStorage.removeItem(SERVER_AUTH_TOKEN_STORAGE_KEY)
    }
    catch {
    }
}

function _handleAuthFailure() {
    clearStoredServerAuthToken()

    if (typeof window === "undefined") return
    if (window.location.pathname.startsWith("/public/auth")) return
    if (authRedirectInProgress) return

    authRedirectInProgress = true
    window.location.replace("/public/auth")
}

function checkAuthError(error: SeaError | AxiosError | null | undefined) {
    if (!error) return

    if (isAuthFailureResponse(error.response?.status, error.response?.data)) {
        _handleAuthFailure()
    }
}

/** 401 PROFILE_REQUIRED / PROFILE_INVALID_TOKEN: the tab's profile session is gone. */
export function isProfileRequiredError(error: unknown): boolean {
    if (!axios.isAxiosError(error)) return false
    const code = getSeaErrorMessage(error.response?.data)
    return error.response?.status === 401 && (code === "PROFILE_REQUIRED" || code === "PROFILE_INVALID_TOKEN")
}

function checkProfileError(error: AxiosError, sentProfileToken: string) {
    // Only drop the session if the request carried the current token (not a stale or override one).
    if (isProfileRequiredError(error) && sentProfileToken && sentProfileToken === getProfileToken()) {
        clearProfileSession()
    }
}

/** The server's error code, if any (e.g. "PROFILE_WRONG_PIN"). */
export function getSeaErrorCode(error: unknown): string {
    if (!axios.isAxiosError(error)) return ""
    return getSeaErrorMessage(error.response?.data)
}

/**
 * Create axios query to the server
 * - First generic: Return type
 * - Second generic: Params/Data type
 */
export async function buildSeaQuery<T, D extends any = any>(
    {
        endpoint,
        method,
        data,
        params,
        password,
        profileToken,
        signal,
    }: SeaQuery<D>): Promise<T | undefined> {
    const headers: Record<string, string> = {}

    if (password) {
        headers["X-Seanime-Token"] = password
    }

    const sentProfileToken = profileToken ?? getProfileToken()
    if (sentProfileToken) {
        headers["X-Seanime-Profile"] = sentProfileToken
    }

    const clientId = getClientId()
    const clientIdProof = getClientIdProof()
    if (clientId) {
        headers["X-Seanime-Client-Id"] = clientId
    }
    if (clientIdProof) {
        headers["X-Seanime-Client-Id-Proof"] = clientIdProof
    }
    if (__clientPlatform__) {
        headers["X-Seanime-Client-Platform"] = __clientPlatform__
    }

    let res
    try {
        res = await axios<T>({
            url: getServerBaseUrl() + endpoint,
            method,
            data,
            params,
            headers,
            withCredentials: true,
            signal,
        })
    }
    catch (error) {
        if (axios.isAxiosError(error)) {
            checkAuthError(error)
            checkProfileError(error, sentProfileToken)
        }
        throw error
    }

    syncClientIdFromHeader(res.headers)

    const response = _handleSeaResponse<T>(res.data)
    return response.data
}

function syncClientIdFromHeader(headers: unknown) {
    if (!headers) return

    const clientId = getHeaderValue(headers, "x-seanime-client-id")
    const clientIdProof = getHeaderValue(headers, "x-seanime-client-id-proof")

    if (clientId) {
        setClientIdentity(clientId, clientIdProof)
    }
}

function getHeaderValue(headers: unknown, name: string): string {
    if (typeof headers === "object" && headers !== null && "get" in headers && typeof headers.get === "function") {
        const value = headers.get(name)
        if (typeof value === "string" && value.trim()) {
            return value.trim()
        }
        return ""
    }

    const rawValue = (headers as Record<string, string | string[] | undefined>)[name]
    if (typeof rawValue === "string" && rawValue.trim()) {
        return rawValue.trim()
    }

    if (Array.isArray(rawValue) && typeof rawValue[0] === "string" && rawValue[0].trim()) {
        return rawValue[0].trim()
    }

    return ""
}

type ServerMutationProps<R, V = void> = UseMutationOptions<R | undefined, SeaError, V, unknown> & {
    endpoint: string
    method: "POST" | "GET" | "PATCH" | "DELETE" | "PUT"
}

/**
 * Create mutation hook to the server
 * - First generic: Return type
 * - Second generic: Params/Data type
 */
export function useServerMutation<R = void, V = void>(
    {
        endpoint,
        method,
        ...options
    }: ServerMutationProps<R, V>) {

    const password = useAtomValue(serverAuthTokenAtom)

    return useMutation<R | undefined, SeaError, V>({
        onError: error => {
            checkAuthError(error)
            if (isAuthFailureResponse(error.response?.status, error.response?.data)) {
                return
            }
            if (isProfileRequiredError(error)) {
                return
            }
            console.log("Mutation error", error)
            const errorMsg = _handleSeaError(error.response?.data)
            if (errorMsg.includes("feature disabled")) {
                toast.warning("This feature is disabled")
                return
            }
            toast.error(errorMsg)
        },
        mutationFn: async (variables) => {
            return buildSeaQuery<R, V>({
                endpoint: endpoint,
                method: method,
                data: variables,
                password: password,
            })
        },
        ...options,
    })
}


type ServerQueryProps<R, V> = UseQueryOptions<R | undefined, SeaError, R | undefined> & {
    endpoint: string
    method: "POST" | "GET" | "PATCH" | "DELETE" | "PUT"
    params?: V
    data?: V
    muteError?: boolean
}

/**
 * Create query hook to the server
 * - First generic: Return type
 * - Second generic: Params/Data type
 */
export function useServerQuery<R, V = any>(
    {
        endpoint,
        method,
        params,
        data,
        muteError,
        ...options
    }: ServerQueryProps<R | undefined, V>) {

    const password = useAtomValue(serverAuthTokenAtom)

    const props = useQuery<R | undefined, SeaError>({
        queryFn: async () => {
            return buildSeaQuery<R, V>({
                endpoint: endpoint,
                method: method,
                params: params,
                data: data,
                password: password,
            })
        },
        ...options,
    })

    useEffect(() => {
        if (!muteError && props.isError) {
            if (isAuthFailureResponse(props.error?.response?.status, props.error?.response?.data)) {
                _handleAuthFailure()
                return
            }
            if (isProfileRequiredError(props.error)) {
                return
            }
            console.log("Server error", props.error)
            const errorMsg = _handleSeaError(props.error?.response?.data)
            if (errorMsg.includes("feature disabled")) {
                return
            }
            if (!!errorMsg) {
                toast.error(errorMsg)
            }
        }
    }, [props.error, props.isError, muteError])

    return props
}

//----------------------------------------------------------------------------------------------------------------------

function _handleSeaError(data: any): string {
    if (typeof data === "string") return "Server Error: " + data

    const err = data?.error as string

    if (!err) return "Unknown error"

    const profileMessage = getProfileErrorMessage(err)
    if (profileMessage) return profileMessage

    if (err.includes("Too many requests"))
        return "AniList: Too many requests, please wait a moment and try again."

    try {
        const graphqlErr = JSON.parse(err) as any
        console.log("AniList error", graphqlErr)
        if (graphqlErr.graphqlErrors && graphqlErr.graphqlErrors.length > 0 && !!graphqlErr.graphqlErrors[0]?.message) {
            return "AniList error: " + graphqlErr.graphqlErrors[0]?.message
        }
        return "AniList error"
    }
    catch (e) {
        if (err.includes("no cached data") || err.includes("cache lookup failed")) {
            return ""
        }
        return "Error: " + err
    }
}

/** User-facing messages for the profile and playback lane error codes. */
export function getProfileErrorMessage(err: string): string {
    const code = err.split(":")[0].trim()
    switch (code) {
        case "PLAYBACK_BUSY":
            return "Someone else is playing something on this server right now. Try again when their playback ends."
        case "LANES_FULL":
            return "All playback slots on this server are in use. Try again when someone stops watching."
        case "PROFILE_REQUIRED":
        case "PROFILE_INVALID_TOKEN":
            return "Choose a profile first."
        case "PROFILE_NOT_FOUND":
            return "This profile doesn't exist anymore."
        case "PROFILE_NAME_TAKEN":
            return "Another profile already has this name."
        case "PROFILE_INVALID_NAME":
            return "Enter a name (up to 32 characters)."
        case "PROFILE_INVALID_AVATAR":
            return "Choose an avatar."
        case "PROFILE_INVALID_COLOR":
            return "Invalid colour."
        case "PROFILE_INVALID_PIN":
            return "The PIN must be 4 to 6 digits."
        case "PROFILE_PIN_REQUIRED":
            return "Enter the PIN."
        case "PROFILE_WRONG_PIN":
            return "Wrong PIN."
        case "PROFILE_PIN_LOCKED":
            return "Too many wrong PINs. Try again in a few minutes."
        case "PROFILE_DELETED":
            return "This profile is in the trash."
        case "PROFILE_NOT_DELETED":
            return "This profile isn't in the trash."
        case "PROFILE_NO_CLIENT_ID":
            return "This device isn't identified yet. Reload the page and try again."
        case "PROFILE_IMPORT_INVALID_USERNAME":
            return "Enter a valid AniList username."
        case "PROFILE_IMPORT_LIST_UNAVAILABLE":
            return "That AniList list isn't available (unknown user or private list)."
        case "PROFILE_IMPORT_INVALID_FILE":
            return "This file isn't a Garceslab Anime profile export or an AniList collection."
        case "PROFILE_IMPORT_NOTHING_TO_IMPORT":
            return "There was nothing to import."
        case "PROFILE_INVALID_SETTING":
            return "Invalid setting: " + err.slice(code.length + 1).trim()
    }
    return ""
}

function _handleSeaResponse<T>(res: unknown): { data: T | undefined, error: string | undefined } {

    if (typeof res === "object" && !!res && "error" in res && typeof res.error === "string") {
        return { data: undefined, error: res.error }
    }
    if (typeof res === "object" && !!res && "data" in res) {
        return { data: res.data as T, error: undefined }
    }

    return { data: undefined, error: "No response from the server" }

}
