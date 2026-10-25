import { type EffectCallback, useEffect } from "react"

/** Runs once on mount (and its cleanup on unmount). For syncing with external systems only. */
export function useMountEffect(effect: EffectCallback) {
	useEffect(effect, [])
}
