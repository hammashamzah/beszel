import { t } from "@lingui/core/macro"
import { atom, onMount } from "nanostores"
import { toast } from "@/components/ui/use-toast"
import { pb } from "@/lib/api"
import { debounce } from "@/lib/utils"

/** A per-app CPU / memory alert rule. App "*" is the system-wide default. */
export interface AppAlertRecord {
	id: string
	user: string
	system: string
	app: string
	/** percent of the host; 0 = off (default rule) or inherit (override) */
	cpu: number
	/** gigabytes; 0 = off (default rule) or inherit (override) */
	memory: number
	/** minutes; 0 = inherit */
	min: number
	disabled: boolean
	/** "config" rules come from the APP_ALERTS_CONFIG file and are read-only */
	source: "" | "ui" | "config"
}

export type AppAlertFields = Partial<Pick<AppAlertRecord, "cpu" | "memory" | "min" | "disabled">>

export const DEFAULT_APP = "*"

const collection = "app_alerts"
const fields = "id,user,system,app,cpu,memory,min,disabled,source"

/** All of the user's app alert rules, loaded and kept live while something is watching. */
export const $appAlerts = atom<AppAlertRecord[]>([])

onMount($appAlerts, () => {
	let active = true
	let unsubscribe: (() => void) | undefined
	pb.collection<AppAlertRecord>(collection)
		.getFullList({ fields })
		.then((records) => active && $appAlerts.set(records))
	pb.collection<AppAlertRecord>(collection)
		.subscribe(
			"*",
			({ action, record }) => {
				const others = $appAlerts.get().filter((r) => r.id !== record.id)
				$appAlerts.set(action === "delete" ? others : [...others, record])
			},
			{ fields }
		)
		.then((unsub) => {
			if (active) {
				unsubscribe = unsub
			} else {
				unsub()
			}
		})
	return () => {
		active = false
		unsubscribe?.()
	}
})

function upsertLocal(record: AppAlertRecord) {
	$appAlerts.set([...$appAlerts.get().filter((r) => r.id !== record.id), record])
}

export async function createAppAlert(system: string, app: string, values: AppAlertFields) {
	try {
		const record = await pb
			.collection<AppAlertRecord>(collection)
			.create({ user: pb.authStore.record?.id, system, app, source: "ui", ...values }, { fields })
		upsertLocal(record)
	} catch (error) {
		failedAppAlertToast(error)
	}
}

export async function deleteAppAlert(id: string) {
	const before = $appAlerts.get()
	$appAlerts.set(before.filter((r) => r.id !== id))
	try {
		await pb.collection(collection).delete(id)
	} catch (error) {
		$appAlerts.set(before)
		failedAppAlertToast(error)
	}
}

export function failedAppAlertToast(error: unknown) {
	console.error(error)
	toast({
		title: t`Failed to update alert`,
		description: t`Please check logs for more details.`,
		variant: "destructive",
	})
}

const pending = new Map<string, AppAlertFields>()
const flush = debounce(() => {
	const batch = [...pending]
	pending.clear()
	for (const [id, values] of batch) {
		pb.collection(collection).update(id, values).catch(failedAppAlertToast)
	}
}, 400)

/** Queue a change to a rule; rapid edits (slider drags, typing) are sent together. */
export function updateAppAlert(id: string, values: AppAlertFields) {
	pending.set(id, { ...pending.get(id), ...values })
	flush()
}

/** The apps currently running on a system: container names up to their first "/". */
export async function getSystemApps(system: string): Promise<string[]> {
	const containers = await pb.collection<{ name: string }>("containers").getFullList({
		fields: "name",
		filter: pb.filter("system={:system}", { system }),
	})
	return [...new Set(containers.map((c) => c.name.split("/")[0]))].sort((a, b) => a.localeCompare(b))
}
