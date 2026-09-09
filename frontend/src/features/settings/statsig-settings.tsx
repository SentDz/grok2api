import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, RefreshCw, RotateCcw } from "lucide-react";
import { type ReactNode } from "react";
import { Controller, useWatch, type UseFormReturn } from "react-hook-form";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { apiRequest } from "@/shared/api/client";
import { createObjectDecoder, hasShape, isArrayOf, isBoolean, isOptional, isString } from "@/shared/api/decoder";
import type { SettingsForm } from "./settings-model";

type Status = { state: string; busy: boolean; activeID: string; source: string; updatedAt?: string; lastChecked?: string; nextCheck?: string; lastError?: string; canRollback: boolean; events: { at: string; action: string; message: string }[] };
const decodeStatus = createObjectDecoder<Status>("Statsig status", {state:isString,busy:isBoolean,activeID:isString,source:isString,updatedAt:isOptional(isString),lastChecked:isOptional(isString),nextCheck:isOptional(isString),lastError:isOptional(isString),canRollback:isBoolean,events:isArrayOf(hasShape({at:isString,action:isString,message:isString}))});

function Row({id,label,error,children}:{id:string;label:string;error?:string;children:ReactNode}) {
  return <div className="grid min-w-0 gap-2 border-b py-3 sm:grid-cols-[180px_minmax(0,1fr)]"><Label htmlFor={id} className="self-center">{label}</Label><div className="min-w-0 space-y-1">{children}{error && <p className="text-xs text-destructive">{error}</p>}</div></div>;
}

export function StatsigSettings({form}:{form:UseFormReturn<SettingsForm>}) {
  const { i18n } = useTranslation();
  const text = (zh:string,en:string) => i18n.language.startsWith("zh") ? zh : en;
  const config = useWatch({control:form.control,name:"providerWeb.statsigBuiltin"});
  const errors = form.formState.errors.providerWeb?.statsigBuiltin;
  const client = useQueryClient();
  const query = useQuery({queryKey:["statsig-status"],queryFn:()=>apiRequest("/api/admin/v1/settings/statsig",{},decodeStatus),refetchInterval:5000});
  const mutation = useMutation({mutationFn:(action:string)=>apiRequest(`/api/admin/v1/settings/statsig/${action}`,{method:"POST"},createObjectDecoder<{accepted:boolean}>("Statsig action",{accepted:isBoolean})),onSuccess:()=>{void client.invalidateQueries({queryKey:["statsig-status"]});toast.success(text("任务已提交","Task queued"));},onError:(error)=>toast.error(error.message)});
  const status = query.data;
  const disabled = mutation.isPending || status?.busy || form.formState.isDirty;
  const date = (value?:string)=>value ? new Date(value).toLocaleString(i18n.language) : "-";
  const stateNames:Record<string,string>={ready:text("已验证","Verified"),unverified:text("未验证","Unverified"),degraded:text("刷新失败","Refresh failed"),invalid:text("签名失效","Invalid")};
  return <div className="min-w-0 sm:col-span-2">
    <Row id="statsig-auto" label={text("自动更新","Automatic updates")}><Controller control={form.control} name="providerWeb.statsigBuiltin.autoUpdate" render={({field})=><Switch id="statsig-auto" checked={field.value} onCheckedChange={field.onChange}/>} /></Row>
    <Row id="statsig-interval" label={text("检测间隔（秒）","Check interval (seconds)")} error={errors?.checkIntervalSeconds?.message}><Input id="statsig-interval" type="number" min={300} max={86400} {...form.register("providerWeb.statsigBuiltin.checkIntervalSeconds",{valueAsNumber:true})}/></Row>
    <Row id="statsig-account" label={text("采集账号 ID","Capture account ID")} error={errors?.captureAccountID?.message}><Input id="statsig-account" type="number" min={0} title={text("0：自动选择 Web 账号","0: automatic Web account selection")} {...form.register("providerWeb.statsigBuiltin.captureAccountID",{valueAsNumber:true})}/></Row>
    <Row id="statsig-llm" label={text("大模型辅助修复","Model-assisted repair")}><Controller control={form.control} name="providerWeb.statsigBuiltin.llmEnabled" render={({field})=><Switch id="statsig-llm" checked={field.value} onCheckedChange={field.onChange}/>} /></Row>
    {config?.llmEnabled && <>
      <Row id="statsig-provider" label={text("模型来源","Model provider")}><Controller control={form.control} name="providerWeb.statsigBuiltin.llmProvider" render={({field})=><Tabs value={field.value} onValueChange={field.onChange}><TabsList id="statsig-provider" className="grid w-full grid-cols-2"><TabsTrigger value="build">{text("本系统 Build 账号","System Build accounts")}</TabsTrigger><TabsTrigger value="external">{text("外部模型","External model")}</TabsTrigger></TabsList></Tabs>}/></Row>
      {config.llmProvider === "external" && <>
        <Row id="statsig-model-url" label={text("模型 API 地址","Model API base URL")} error={errors?.llmURL?.message}><Input id="statsig-model-url" type="url" placeholder="https://api.example.com/v1" {...form.register("providerWeb.statsigBuiltin.llmURL")}/></Row>
        <Row id="statsig-model-key" label="API Key" error={errors?.llmKey?.message}><Input id="statsig-model-key" type="password" autoComplete="new-password" placeholder={config.llmKeyConfigured ? text("已配置","Configured") : ""} {...form.register("providerWeb.statsigBuiltin.llmKey")}/></Row>
        {config.llmKeyConfigured && <Row id="statsig-clear-key" label={text("清除已保存 Key","Clear saved key")}><Controller control={form.control} name="providerWeb.statsigBuiltin.clearLLMKey" render={({field})=><Switch id="statsig-clear-key" checked={field.value} onCheckedChange={field.onChange}/>} /></Row>}
      </>}
      <Row id="statsig-model" label={text("模型名称","Model name")} error={errors?.llmModel?.message}><Input id="statsig-model" {...form.register("providerWeb.statsigBuiltin.llmModel")}/></Row>
      <Row id="statsig-attempts" label={text("修复尝试上限","Maximum repair attempts")} error={errors?.llmMaxAttempts?.message}><Input id="statsig-attempts" type="number" min={1} max={8} {...form.register("providerWeb.statsigBuiltin.llmMaxAttempts",{valueAsNumber:true})}/></Row>
    </>}
    <div className="space-y-3 py-4 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2"><span>{text("签名状态","Signature status")}: {status?.busy ? text("处理中","Processing") : stateNames[status?.state ?? ""] ?? "-"}</span><div className="flex gap-1">{[{action:"check",Icon:Check,label:text("立即验证","Verify now")},{action:"refresh",Icon:RefreshCw,label:text("采集并修复","Capture and repair")},{action:"rollback",Icon:RotateCcw,label:text("回退版本","Roll back")}].map(({action,Icon,label})=><Tooltip key={action}><TooltipTrigger asChild><Button type="button" variant="outline" size="icon" aria-label={label} disabled={disabled || (action==="rollback" && !status?.canRollback)} onClick={()=>mutation.mutate(action)}><Icon className="size-4"/></Button></TooltipTrigger><TooltipContent>{label}</TooltipContent></Tooltip>)}</div></div>
      {query.isError && <p role="alert" className="text-destructive">{query.error.message}</p>}
      {status?.lastError && <p role="alert" className="break-words text-destructive">{status.lastError}</p>}
      <dl className="grid min-w-0 grid-cols-1 gap-2 text-xs text-muted-foreground sm:grid-cols-2"><div><dt>{text("当前版本","Current version")}</dt><dd className="break-all">{status?.activeID || "-"}</dd></div><div><dt>{text("最近成功","Last success")}</dt><dd>{date(status?.updatedAt)}</dd></div><div><dt>{text("最近检查","Last checked")}</dt><dd>{date(status?.lastChecked)}</dd></div><div><dt>{text("下次检查","Next check")}</dt><dd>{date(status?.nextCheck)}</dd></div></dl>
      {status?.events.length ? <details><summary className="cursor-pointer">{text("更新记录","Update history")}</summary><ul className="mt-2 divide-y">{[...status.events].reverse().map((event,i)=><li key={`${event.at}-${i}`} className="break-words py-2 text-xs"><time className="text-muted-foreground">{date(event.at)}</time><p>{event.message}</p></li>)}</ul></details> : null}
    </div>
  </div>;
}
