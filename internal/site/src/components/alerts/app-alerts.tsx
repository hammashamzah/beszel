import { t } from "@lingui/core/macro"
import { Plural, Trans } from "@lingui/react/macro"
import { useStore } from "@nanostores/react"
import { BoxesIcon, LockIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { lazy, Suspense, useId, useState } from "react"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
	$appAlerts,
	type AppAlertRecord,
	createAppAlert,
	DEFAULT_APP,
	deleteAppAlert,
	getSystemApps,
	updateAppAlert,
} from "@/lib/app-alerts"
import { cn } from "@/lib/utils"
import type { SystemRecord } from "@/types"

const Slider = lazy(() => import("@/components/ui/slider"))

/** Values a new default rule starts with. */
const NEW_DEFAULT = { cpu: 50, memory: 2, min: 10 }

/**
 * Per-app CPU and memory alerts for one system: a default rule for every app,
 * plus overrides for individual apps.
 */
export function AppAlerts({ system }: { system: SystemRecord }) {
	const rules = useStore($appAlerts).filter((r) => r.system === system.id)
	const defaultRule = rules.find((r) => r.app === DEFAULT_APP)
	const overrides = rules.filter((r) => r.app !== DEFAULT_APP).sort((a, b) => a.app.localeCompare(b.app))
	const enabled = !!defaultRule || overrides.length > 0
	const fromConfig = defaultRule?.source === "config"

	return (
		<div className="rounded-lg border border-muted-foreground/15 hover:border-muted-foreground/20 transition-colors duration-100">
			<label
				htmlFor="s-app-alerts"
				className={cn(
					"flex flex-row items-center justify-between gap-4 p-4",
					!fromConfig && "cursor-pointer",
					enabled && "pb-0"
				)}
			>
				<div className="grid gap-1 select-none">
					<p className="font-semibold flex gap-3 items-center">
						<BoxesIcon className="h-4 w-4 opacity-85" /> <Trans>App Usage</Trans>
						{fromConfig && <ConfigBadge />}
					</p>
					{!enabled && (
						<span className="block text-sm text-muted-foreground">
							<Trans>Triggers when one app's CPU or memory exceeds a threshold</Trans>
						</span>
					)}
				</div>
				<Switch
					id="s-app-alerts"
					checked={!!defaultRule}
					disabled={fromConfig}
					onCheckedChange={(checked) => {
						if (checked) {
							createAppAlert(system.id, DEFAULT_APP, NEW_DEFAULT)
						} else if (defaultRule) {
							deleteAppAlert(defaultRule.id)
						}
					}}
				/>
			</label>
			{enabled && (
				<div className="px-4 pb-5 mt-1.5 grid gap-5">
					{defaultRule ? (
						<DefaultRule key={defaultRule.id} rule={defaultRule} />
					) : (
						<p className="text-sm text-muted-foreground">
							<Trans>No default rule: only the apps below are watched.</Trans>
						</p>
					)}
					<Overrides system={system} defaultRule={defaultRule} overrides={overrides} />
				</div>
			)}
		</div>
	)
}

function ConfigBadge() {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex items-center gap-1 rounded-sm border border-muted-foreground/20 px-1.5 py-0.5 text-[0.7rem] font-medium text-muted-foreground">
					<LockIcon className="size-3" />
					<Trans context="Set in the app alerts config file">config</Trans>
				</span>
			</TooltipTrigger>
			<TooltipContent>
				<Trans>Set in the app alerts config file. Change it there.</Trans>
			</TooltipContent>
		</Tooltip>
	)
}

function DefaultRule({ rule }: { rule: AppAlertRecord }) {
	const [cpu, setCpu] = useState(rule.cpu)
	const [memory, setMemory] = useState(rule.memory)
	const [min, setMin] = useState(rule.min || 10)
	const readOnly = rule.source === "config"

	return (
		<div className="grid sm:grid-cols-2 gap-5 tabular-nums text-muted-foreground">
			<Suspense fallback={<div className="h-10" />}>
				<Threshold
					id={`cpu-${rule.id}`}
					label={
						cpu ? (
							<Trans>
								CPU exceeds <strong className="text-foreground">{cpu}%</strong>
							</Trans>
						) : (
							<Trans>CPU limit off</Trans>
						)
					}
					value={cpu}
					max={100}
					step={1}
					disabled={readOnly}
					onChange={setCpu}
					onCommit={(v) => updateAppAlert(rule.id, { cpu: v })}
				/>
				<Threshold
					id={`mem-${rule.id}`}
					label={
						memory ? (
							<Trans>
								Memory exceeds <strong className="text-foreground">{memory} GB</strong>
							</Trans>
						) : (
							<Trans>Memory limit off</Trans>
						)
					}
					value={memory}
					max={64}
					step={0.5}
					disabled={readOnly}
					onChange={setMemory}
					onCommit={(v) => updateAppAlert(rule.id, { memory: v })}
				/>
				<Threshold
					id={`min-${rule.id}`}
					label={
						<Trans>
							For <strong className="text-foreground">{min}</strong> <Plural value={min} one="minute" other="minutes" />
						</Trans>
					}
					value={min}
					min={1}
					max={60}
					step={1}
					disabled={readOnly}
					onChange={setMin}
					onCommit={(v) => updateAppAlert(rule.id, { min: v })}
				/>
			</Suspense>
			<p className="text-sm sm:col-span-2 -mt-2">
				<Trans>CPU is a share of the whole host, as in the containers table. Set a limit to 0 to turn it off.</Trans>
			</p>
		</div>
	)
}

function Threshold({
	id,
	label,
	value,
	min = 0,
	max,
	step,
	disabled,
	onChange,
	onCommit,
}: {
	id: string
	label: React.ReactNode
	value: number
	min?: number
	max: number
	step: number
	disabled?: boolean
	onChange: (value: number) => void
	onCommit: (value: number) => void
}) {
	return (
		<div>
			<p id={id} className="text-sm block h-6">
				{label}
			</p>
			<div className="flex gap-3 items-center">
				<Slider
					aria-labelledby={id}
					value={[value]}
					onValueChange={(v) => onChange(v[0])}
					onValueCommit={(v) => onCommit(v[0])}
					min={min}
					max={max}
					step={step}
					disabled={disabled}
					className={cn(disabled && "opacity-50")}
				/>
				<Input
					type="number"
					value={value}
					onChange={(e) => {
						const v = parseFloat(e.target.value)
						if (!Number.isNaN(v)) {
							const clamped = Math.max(min, Math.min(v, max))
							onChange(clamped)
							onCommit(clamped)
						}
					}}
					min={min}
					max={max}
					step={step}
					disabled={disabled}
					className="w-16 h-8 text-center px-1"
				/>
			</div>
		</div>
	)
}

function Overrides({
	system,
	defaultRule,
	overrides,
}: {
	system: SystemRecord
	defaultRule?: AppAlertRecord
	overrides: AppAlertRecord[]
}) {
	const [apps, setApps] = useState<string[] | null>(null)
	const taken = new Set(overrides.map((r) => r.app))
	const available = apps?.filter((app) => !taken.has(app))

	return (
		<div className="grid gap-2">
			<div className="flex items-center justify-between gap-3">
				<p className="text-sm font-medium text-foreground">
					<Trans>Per-app overrides</Trans>
				</p>
				<DropdownMenu onOpenChange={(open) => open && getSystemApps(system.id).then(setApps)}>
					<DropdownMenuTrigger asChild>
						<Button variant="outline" size="sm" className="h-8 gap-1.5">
							<PlusIcon className="size-3.5" />
							<Trans>Add app</Trans>
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" className="max-h-72 overflow-auto">
						{!available && (
							<DropdownMenuItem disabled>
								<Trans>Loading...</Trans>
							</DropdownMenuItem>
						)}
						{available?.length === 0 && (
							<DropdownMenuItem disabled>
								<Trans>No other running apps</Trans>
							</DropdownMenuItem>
						)}
						{available?.map((app) => (
							<DropdownMenuItem key={app} className="min-w-48" onSelect={() => createAppAlert(system.id, app, {})}>
								{app}
							</DropdownMenuItem>
						))}
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
			{overrides.length === 0 ? (
				<p className="text-sm text-muted-foreground">
					<Trans>Give an app its own limits, or mute it.</Trans>
				</p>
			) : (
				<div className="grid divide-y divide-muted-foreground/15 rounded-md border border-muted-foreground/15">
					{overrides.map((rule) => (
						<OverrideRow key={rule.id} rule={rule} defaultRule={defaultRule} />
					))}
				</div>
			)}
		</div>
	)
}

function OverrideRow({ rule, defaultRule }: { rule: AppAlertRecord; defaultRule?: AppAlertRecord }) {
	const readOnly = rule.source === "config"
	const muted = rule.disabled
	const inherited = (value: number | undefined, unit = "") => (value ? `${value}${unit}` : t`off`)

	return (
		<div className="grid gap-2.5 px-3 py-2.5">
			<div className="flex items-center gap-2">
				<span className="font-medium text-sm truncate" title={rule.app}>
					{rule.app}
				</span>
				{readOnly && <ConfigBadge />}
				<span className="ms-auto flex items-center gap-1.5">
					<Switch
						checked={!muted}
						disabled={readOnly}
						onCheckedChange={(on) => updateAppAlert(rule.id, { disabled: !on })}
						aria-label={t`Alerts for ${rule.app}`}
					/>
					{!readOnly && (
						<Button
							variant="ghost"
							size="icon"
							className="size-8 text-muted-foreground"
							aria-label={t`Remove override for ${rule.app}`}
							onClick={() => deleteAppAlert(rule.id)}
						>
							<Trash2Icon className="size-4" />
						</Button>
					)}
				</span>
			</div>
			{muted ? (
				<p className="text-sm text-muted-foreground">
					<Trans>Muted: no alerts for this app.</Trans>
				</p>
			) : (
				<div className="grid grid-cols-3 gap-2 text-muted-foreground">
					<OverrideField
						label={t`CPU %`}
						value={rule.cpu}
						placeholder={inherited(defaultRule?.cpu, "%")}
						max={100}
						step={1}
						disabled={readOnly}
						onCommit={(cpu) => updateAppAlert(rule.id, { cpu })}
					/>
					<OverrideField
						label={t`Memory GB`}
						value={rule.memory}
						placeholder={inherited(defaultRule?.memory, " GB")}
						step={0.5}
						disabled={readOnly}
						onCommit={(memory) => updateAppAlert(rule.id, { memory })}
					/>
					<OverrideField
						label={t`Minutes`}
						value={rule.min}
						placeholder={inherited(defaultRule?.min || 10)}
						max={60}
						step={1}
						integer
						disabled={readOnly}
						onCommit={(min) => updateAppAlert(rule.id, { min })}
					/>
				</div>
			)}
		</div>
	)
}

/** A number input where empty means "use the default rule's value". */
function OverrideField({
	label,
	value,
	placeholder,
	max,
	step,
	integer,
	disabled,
	onCommit,
}: {
	label: string
	value: number
	placeholder: string
	max?: number
	step: number
	integer?: boolean
	disabled?: boolean
	onCommit: (value: number) => void
}) {
	const [text, setText] = useState(value ? String(value) : "")
	const id = useId()
	return (
		<div className="grid gap-1 text-xs">
			<label htmlFor={id}>{label}</label>
			<Input
				id={id}
				type="number"
				inputMode="decimal"
				value={text}
				placeholder={placeholder}
				min={0}
				max={max}
				step={step}
				disabled={disabled}
				className="h-8 px-2 tabular-nums placeholder:text-muted-foreground/60"
				onChange={(e) => {
					setText(e.target.value)
					const parsed = e.target.value === "" ? 0 : integer ? parseInt(e.target.value, 10) : parseFloat(e.target.value)
					if (!Number.isNaN(parsed)) {
						onCommit(Math.max(0, max != null ? Math.min(parsed, max) : parsed))
					}
				}}
			/>
		</div>
	)
}
