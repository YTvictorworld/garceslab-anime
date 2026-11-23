import { Button } from "@/components/ui/button"
import { cn } from "@/components/ui/core/styling"
import React from "react"
import { LuDelete } from "react-icons/lu"

type PinPadProps = {
    /** Called when the user confirms (Enter / OK) or reaches maxLength digits. */
    onSubmit: (pin: string) => void
    disabled?: boolean
    error?: string
    minLength?: number
    maxLength?: number
    /** Changing this key clears the typed digits (e.g. after a wrong PIN). */
    resetKey?: number
}

/** Numeric keypad for 4-6 digit PINs. Also accepts the keyboard's digits. */
export function PinPad({ onSubmit, disabled, error, minLength = 4, maxLength = 6, resetKey }: PinPadProps) {
    const [pin, setPin] = React.useState("")

    React.useEffect(() => setPin(""), [resetKey])

    const submit = React.useCallback((value: string) => {
        if (disabled || value.length < minLength) return
        onSubmit(value)
    }, [disabled, minLength, onSubmit])

    const press = React.useCallback((digit: string) => {
        if (disabled) return
        setPin(prev => {
            if (prev.length >= maxLength) return prev
            const next = prev + digit
            if (next.length === maxLength) {
                // Defer so the last dot renders before submitting.
                setTimeout(() => submit(next), 0)
            }
            return next
        })
    }, [disabled, maxLength, submit])

    const backspace = React.useCallback(() => {
        if (disabled) return
        setPin(prev => prev.slice(0, -1))
    }, [disabled])

    React.useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key >= "0" && e.key <= "9") {
                e.preventDefault()
                press(e.key)
            } else if (e.key === "Backspace") {
                e.preventDefault()
                backspace()
            } else if (e.key === "Enter") {
                e.preventDefault()
                submit(pin)
            }
        }
        window.addEventListener("keydown", onKey)
        return () => window.removeEventListener("keydown", onKey)
    }, [press, backspace, submit, pin])

    return (
        <div data-pin-pad className="flex flex-col items-center gap-4">
            <div className="flex gap-3 h-6 items-center" aria-label={`${pin.length} digits entered`}>
                {Array.from({ length: Math.max(minLength, pin.length) }).map((_, i) => (
                    <span
                        key={i}
                        className={cn(
                            "size-3.5 rounded-full border border-[--border] transition-colors",
                            i < pin.length && "bg-white border-white",
                            !!error && "border-[--red]",
                        )}
                    />
                ))}
            </div>
            <p className={cn("text-sm h-5", error ? "text-[--red]" : "text-[--muted]")}>
                {error || "Enter the PIN"}
            </p>
            <div className="grid grid-cols-3 gap-3">
                {["1", "2", "3", "4", "5", "6", "7", "8", "9"].map(d => (
                    <Button
                        key={d}
                        intent="gray-subtle"
                        size="lg"
                        className="w-16 h-16 text-xl"
                        disabled={disabled}
                        onClick={() => press(d)}
                        data-pin-digit={d}
                    >{d}</Button>
                ))}
                <Button
                    intent="gray-basic"
                    size="lg"
                    className="w-16 h-16"
                    disabled={disabled}
                    onClick={backspace}
                    aria-label="Delete"
                ><LuDelete className="text-xl" /></Button>
                <Button
                    intent="gray-subtle"
                    size="lg"
                    className="w-16 h-16 text-xl"
                    disabled={disabled}
                    onClick={() => press("0")}
                    data-pin-digit="0"
                >0</Button>
                <Button
                    intent="primary"
                    size="lg"
                    className="w-16 h-16"
                    disabled={disabled || pin.length < minLength}
                    onClick={() => submit(pin)}
                    data-pin-ok
                >OK</Button>
            </div>
        </div>
    )
}
