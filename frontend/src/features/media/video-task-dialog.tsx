import { useQuery } from "@tanstack/react-query";
import { AlertCircle, CheckCircle2, Clock, Loader2, RefreshCw } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { getVideoDetail } from "@/features/media/media-api";
import { ErrorState } from "@/shared/components/data-state";
import { cn } from "@/shared/lib/cn";
import { formatDateTime } from "@/shared/lib/format";

function elapsed(start: string, end: string): string {
  const milliseconds = Math.max(0, Date.parse(end) - Date.parse(start));
  if (!Number.isFinite(milliseconds)) return "-";
  if (milliseconds < 1000) return `${milliseconds} ms`;
  const seconds = Math.floor(milliseconds / 1000);
  return `${Math.floor(seconds / 3600).toString().padStart(2, "0")}:${Math.floor(seconds / 60 % 60).toString().padStart(2, "0")}:${(seconds % 60).toString().padStart(2, "0")}.${(milliseconds % 1000).toString().padStart(3, "0")}`;
}

export function VideoTaskDialog({ jobId, onClose }: { jobId: string | null; onClose: () => void }) {
  const { t, i18n } = useTranslation();
  const detail = useQuery({
    queryKey: ["media", "videos", "detail", jobId],
    queryFn: () => getVideoDetail(jobId!),
    enabled: Boolean(jobId),
    refetchInterval: (query) => jobId && (!query.state.data || ["queued", "in_progress"].includes(query.state.data.status)) ? 3_000 : false,
  });
  const job = detail.data;
  const events = job?.diagnostics.events ?? [];
  const current = events.at(-1);
  const failure = job?.status === "failed" && current?.error ? current : undefined;
  const active = job?.status === "queued" || job?.status === "in_progress";
  const stale = job && active && Date.parse(job.serverTime) - Date.parse(job.updatedAt) >= 120_000;
  const expired = job && job.status === "in_progress" && job.leaseUntil && Date.parse(job.leaseUntil) <= Date.parse(job.serverTime);
  const stageLabel = (stage: string) => t(`videoTask.stages.${stage}`, { defaultValue: stage });
  const date = (value: string) => formatDateTime(value, i18n.language);
  const preciseDate = (value: string) => new Date(value).toLocaleString(i18n.language, {
    year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", fractionalSecondDigits: 3, hour12: false,
  });

  return (
    <Dialog open={Boolean(jobId)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-3xl overflow-y-auto">
        <DialogHeader className="min-w-0 pr-8 text-left">
          <DialogTitle>{t("videoTask.title")}</DialogTitle>
          <DialogDescription className="break-all font-mono text-[11px]">{jobId}</DialogDescription>
        </DialogHeader>
        {detail.isPending ? <div className="flex h-32 items-center justify-center"><Loader2 className="size-5 animate-spin" aria-label={t("common.loading")} /></div> : null}
        {detail.isError ? <ErrorState message={detail.error.message} onRetry={() => void detail.refetch()} /> : null}
        {job ? (
          <div className="min-w-0 space-y-5 text-xs">
            <div className="flex items-start justify-between gap-3 border-b pb-4">
              <div className="min-w-0 space-y-2" aria-live="polite">
                <div className="flex flex-wrap items-center gap-2 font-medium">
                  {job.status === "failed" ? <AlertCircle className="size-4 text-destructive" /> : active ? <Clock className="size-4 text-muted-foreground" /> : <CheckCircle2 className="size-4 text-emerald-600" />}
                  <span>{t(`media.videoStatus.${job.status}`)}</span>
                  <span className="tabular-nums text-muted-foreground">{job.progress}%</span>
                </div>
                <p className="break-words text-sm font-medium">{active && !job.diagnosticsEnabled ? t("videoTask.recordingDisabled") : current ? stageLabel(current.stage) : job.status === "queued" ? t("videoTask.stages.queued") : t("videoTask.unknown")}{current && current.itemTotal > 0 && (!active || job.diagnosticsEnabled) ? ` (${current.itemIndex}/${current.itemTotal})` : ""}</p>
                {current && (!active || job.diagnosticsEnabled) ? <p className="text-muted-foreground">{t("videoTask.stageElapsed")}: <span className="font-mono">{elapsed(current.startedAt, current.finishedAt ?? job.completedAt ?? job.serverTime)}</span></p> : null}
                {active && current?.deadlineAt && job.diagnosticsEnabled ? <p className="break-words text-muted-foreground">{t("videoTask.requestDeadline")}: {preciseDate(current.deadlineAt)}</p> : null}
              </div>
              <Tooltip>
                <TooltipTrigger asChild><Button variant="ghost" size="icon" className="size-8 shrink-0" disabled={detail.isFetching} onClick={() => void detail.refetch()} aria-label={t("common.refresh")}><RefreshCw className={detail.isFetching ? "animate-spin" : undefined} /></Button></TooltipTrigger>
                <TooltipContent>{t("common.refresh")}</TooltipContent>
              </Tooltip>
            </div>
            {expired || (stale && job.diagnosticsEnabled) ? <p role="status" className="break-words border-l-2 border-amber-500 pl-3 text-amber-700 dark:text-amber-400">{expired ? t("videoTask.leaseExpired") : t("videoTask.stale", { duration: elapsed(job.updatedAt, job.serverTime) })}</p> : null}
            {!events.length ? <p className="text-muted-foreground">{t("videoTask.noHistory")}</p> : null}
            {failure || job.errorMessage ? <div className="space-y-1 border-l-2 border-destructive pl-3"><p className="font-medium text-destructive">{t("videoTask.error")}{job.errorCode ? ` (${job.errorCode})` : ""}</p><p className="whitespace-pre-wrap break-all">{failure?.httpStatus ? `HTTP ${failure.httpStatus} · ` : ""}{failure?.error || job.errorMessage}</p></div> : null}
            <dl className="grid min-w-0 grid-cols-1 gap-x-6 gap-y-3 sm:grid-cols-2">
              {[
                [t("videoTask.account"), job.accountName ? `${job.accountName} (#${job.accountId})` : "-"],
                [t("videoTask.attempts"), String(job.diagnostics.attempt)],
                [t("videoTask.provider"), `${job.provider} / ${job.upstreamModel}`],
                [t("videoTask.egress"), job.egressMode === "direct" ? t("videoTask.direct") : job.egressNodeName || "-"],
                [t("media.videos.createdAt"), date(job.createdAt)],
                [t("videoTask.updatedAt"), date(job.updatedAt)],
                [t("videoTask.totalElapsed"), elapsed(job.createdAt, job.completedAt ?? job.serverTime)],
                [t("videoTask.leaseUntil"), job.leaseUntil ? date(job.leaseUntil) : "-"],
                [t("videoTask.requestId"), job.requestId || "-"],
                [t("media.videos.spec"), `${job.quality} / ${job.size} / ${t("media.videos.seconds", { count: job.seconds })}`],
              ].map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-muted-foreground">{label}</dt><dd className="mt-1 break-all">{value}</dd></div>)}
            </dl>
            {events.length ? (
              <section className="border-t pt-4">
                <h3 className="mb-3 text-xs font-medium">{t("videoTask.history")}</h3>
                <ol className="space-y-0">
                  {[...events].reverse().map((event, index) => (
                    <li key={`${event.startedAt}-${index}`} className="relative flex min-w-0 gap-3 pb-4 last:pb-0">
                      <div className="relative flex w-4 shrink-0 justify-center">
                        {index < events.length - 1 ? <span className="absolute bottom-0 top-5 w-px bg-border" /> : null}
                        {event.error ? <AlertCircle className="size-4 text-destructive" /> : event.finishedAt ? <CheckCircle2 className="size-4 text-emerald-600" /> : <Clock className="size-4 text-muted-foreground" />}
                      </div>
                      <div className="min-w-0 flex-1 space-y-1">
                        <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                          <span className={cn("font-medium", event.error && "text-destructive")}>{stageLabel(event.stage)}{event.itemTotal > 0 ? ` (${event.itemIndex}/${event.itemTotal})` : ""}</span>
                          <span className="font-mono text-[11px] tabular-nums text-muted-foreground">{!event.finishedAt && !job.diagnosticsEnabled ? "-" : elapsed(event.startedAt, event.finishedAt ?? job.completedAt ?? job.serverTime)}</span>
                        </div>
                        <p className="break-words text-[11px] text-muted-foreground">{preciseDate(event.startedAt)}{event.attempt > 0 ? ` · ${t("videoTask.attempt", { count: event.attempt })}` : ""}{event.accountName ? ` · ${event.accountName}` : ""}</p>
                        {event.request || event.egressMode || event.httpStatus ? <p className="break-words text-[11px] text-muted-foreground">{[
                          event.request ? t(`videoTask.requests.${event.request}`, { defaultValue: event.request }) : "",
                          event.egressMode === "direct" ? t("videoTask.direct") : event.egressMode === "proxy" ? `${t("videoTask.proxy")} · ${event.egressNodeName || `#${event.egressNodeId}`}` : "",
                          event.egressScope || "",
                          event.httpStatus ? `HTTP ${event.httpStatus}` : "",
                        ].filter(Boolean).join(" · ")}</p> : null}
                        {event.error ? <p className="whitespace-pre-wrap break-all text-destructive">{event.httpStatus ? `HTTP ${event.httpStatus} · ` : ""}{event.error}</p> : null}
                      </div>
                    </li>
                  ))}
                </ol>
              </section>
            ) : null}
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
