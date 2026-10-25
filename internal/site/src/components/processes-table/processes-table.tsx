import { t } from "@lingui/core/macro"
import { Trans } from "@lingui/react/macro"
import { ArrowDownIcon, ArrowUpIcon } from "lucide-react"
import { listenKeys } from "nanostores"
import { useState } from "react"
import { Card, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { pb } from "@/lib/api"
import { $allSystemsById } from "@/lib/stores"
import { useMountEffect } from "@/lib/use-mount-effect"
import { cn, decimalString, formatBytes } from "@/lib/utils"
import type { ProcessesRecord, ProcRecord } from "@/types"

type SortKey = "p" | "u" | "n" | "g" | "c" | "m" | "t"

const columns: { key: SortKey; label: () => string; numeric?: boolean }[] = [
	{ key: "p", label: () => t`PID`, numeric: true },
	{ key: "u", label: () => t`User` },
	{ key: "n", label: () => t`Command` },
	{ key: "g", label: () => t`Group` },
	{ key: "c", label: () => t`CPU`, numeric: true },
	{ key: "m", label: () => t`Memory`, numeric: true },
	{ key: "t", label: () => t`Running`, numeric: true },
]

/** "3d 4h", "12m": how long a process has been running */
function runningFor(start?: number) {
	if (!start) return ""
	const secs = Math.max(0, Date.now() / 1000 - start)
	const d = Math.floor(secs / 86400)
	const h = Math.floor((secs % 86400) / 3600)
	const m = Math.floor((secs % 3600) / 60)
	if (d) return `${d}d ${h}h`
	if (h) return `${h}h ${m}m`
	return `${m}m`
}

/**
 * The agent's top processes, by CPU and by memory, like htop. Uses the realtime
 * list while the 1 minute chart is open, otherwise the list the hub stored on
 * the last agent update. Mount with key={systemId}.
 */
export default function ProcessesTable({ systemId, liveProcs }: { systemId: string; liveProcs: ProcRecord[] | null }) {
	const [stored, setStored] = useState<ProcessesRecord | null>(null)
	const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: "c", desc: true })
	const [filter, setFilter] = useState("")

	useMountEffect(() => {
		const fetchProcs = () =>
			pb
				.collection<ProcessesRecord>("processes")
				.getOne(systemId, { fields: "procs,updated", requestKey: null })
				.then(setStored)
				.catch(() => {})
		fetchProcs()
		// the hub stores a new list on every agent update, which also updates the system
		return listenKeys($allSystemsById, [systemId], fetchProcs)
	})

	const procs = liveProcs ?? stored?.procs ?? []
	if (!procs.length) {
		return null
	}

	const terms = filter.toLowerCase().split(" ").filter(Boolean)
	const rows = procs
		.filter((p) => {
			if (!terms.length) return true
			const hay = `${p.p} ${p.u} ${p.n} ${p.g} ${p.cmd ?? ""}`.toLowerCase()
			return terms.every((term) => hay.includes(term))
		})
		.sort((a, b) => {
			const key = sort.key
			// "running" sorts by start time, newest first when ascending
			const av = key === "t" ? -(a.t ?? 0) : a[key]
			const bv = key === "t" ? -(b.t ?? 0) : b[key]
			const cmp = typeof av === "number" && typeof bv === "number" ? av - bv : String(av).localeCompare(String(bv))
			return sort.desc ? -cmp : cmp
		})

	const totalCpu = procs.reduce((sum, p) => sum + p.c, 0)
	const totalMem = procs.reduce((sum, p) => sum + p.m, 0)
	const mem = formatBytes(totalMem)

	const toggleSort = (key: SortKey, numeric?: boolean) =>
		setSort((cur) => (cur.key === key ? { key, desc: !cur.desc } : { key, desc: !!numeric }))

	return (
		<Card className="@container w-full px-3 py-5 sm:py-6 sm:px-6">
			<CardHeader className="p-0 mb-3 sm:mb-4">
				<div className="grid md:flex gap-x-5 gap-y-3 w-full items-end">
					<div className="px-2 sm:px-1">
						<CardTitle className="mb-2">
							<Trans>Processes</Trans>
						</CardTitle>
						<div className="text-sm text-muted-foreground flex items-center flex-wrap">
							<Trans>Top {procs.length} by CPU and memory</Trans>
							<Separator orientation="vertical" className="h-4 mx-2 bg-primary/40" />
							<Trans>
								CPU {decimalString(totalCpu, 1)}%, memory {decimalString(mem.value, 1)} {mem.unit}
							</Trans>
							<Separator orientation="vertical" className="h-4 mx-2 bg-primary/40" />
							{liveProcs ? <Trans>Live</Trans> : <Trans>Updated every minute</Trans>}
						</div>
					</div>
					<Input
						placeholder={t`Filter...`}
						value={filter}
						onChange={(e) => setFilter(e.target.value)}
						className="ms-auto px-4 w-full max-w-full md:w-64"
					/>
				</div>
			</CardHeader>
			<div className="max-h-[calc(100dvh-17rem)] max-w-full overflow-auto border rounded-md">
				<Table className="text-sm">
					<TableHeader className="sticky top-0 z-10 bg-card">
						<TableRow>
							{columns.map((col) => (
								<TableHead key={col.key} className={cn("px-3 whitespace-nowrap", col.numeric && "text-end")}>
									<button
										type="button"
										className="inline-flex items-center gap-1 hover:text-foreground"
										onClick={() => toggleSort(col.key, col.numeric)}
									>
										{col.label()}
										{sort.key === col.key &&
											(sort.desc ? <ArrowDownIcon className="size-3" /> : <ArrowUpIcon className="size-3" />)}
									</button>
								</TableHead>
							))}
						</TableRow>
					</TableHeader>
					<TableBody>
						{rows.map((p) => {
							const m = formatBytes(p.m)
							return (
								<TableRow key={`${p.p}-${p.t ?? 0}`}>
									<TableCell className="px-3 py-2 text-end tabular-nums text-muted-foreground">{p.p}</TableCell>
									<TableCell className="px-3 py-2">{p.u}</TableCell>
									<TableCell className="px-3 py-2 max-w-[28rem]">
										<div className="font-medium">{p.n}</div>
										{p.cmd && (
											<div className="text-xs text-muted-foreground truncate" title={p.cmd}>
												{p.cmd}
											</div>
										)}
									</TableCell>
									<TableCell className="px-3 py-2 whitespace-nowrap">{p.g}</TableCell>
									<TableCell className="px-3 py-2 text-end tabular-nums">{decimalString(p.c, 2)}%</TableCell>
									<TableCell className="px-3 py-2 text-end tabular-nums whitespace-nowrap">
										{decimalString(m.value, m.value >= 10 ? 0 : 1)} {m.unit}
									</TableCell>
									<TableCell className="px-3 py-2 text-end tabular-nums whitespace-nowrap text-muted-foreground">
										{runningFor(p.t)}
									</TableCell>
								</TableRow>
							)
						})}
					</TableBody>
				</Table>
			</div>
		</Card>
	)
}
