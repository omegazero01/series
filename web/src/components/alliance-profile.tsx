"use client";

// 联盟画像编辑与建议审核组件（fork-only: feat/alliance-profile）。
// 画像文档按三阶段展开；保存带 expected_version，冲突时提示重新读取。

import * as React from "react";

import { HistoryIcon, LightbulbIcon, PlusIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { documentStats, fieldBucket, firstIncompletePhase } from "@/lib/alliance-profile-stats";
import { api } from "@/lib/api";
import type {
  AllianceProfileDocument,
  AllianceProfileField,
  AllianceProfileSection,
  AllianceRevision,
  AllianceSuggestion,
  AllianceTaskReading,
} from "@/lib/types";

const STATUS_LABELS: Record<string, string> = {
  unknown: "未知/待确认",
  supplied: "已提供未复核",
  confirmed: "人工确认",
  not_applicable: "不适用",
};

const STATUS_ORDER = ["unknown", "supplied", "confirmed", "not_applicable"];

export function statusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status;
}

function profileSnapshotKey(document: AllianceProfileDocument | null, environment: string): string {
  return JSON.stringify({ document, environment });
}

export function AllianceProfileDialog({
  systemId,
  systemName,
  open,
  onOpenChange,
  onSaved,
  startAtIncomplete = false,
}: {
  systemId: number;
  systemName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved?: () => void;
  startAtIncomplete?: boolean;
}) {
  const [document, setDocument] = React.useState<AllianceProfileDocument | null>(null);
  const [environment, setEnvironment] = React.useState("");
  const [version, setVersion] = React.useState(0);
  const [phase, setPhase] = React.useState("1");
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [historyOpen, setHistoryOpen] = React.useState(false);
  const [history, setHistory] = React.useState<AllianceRevision[]>([]);
  const [suggestions, setSuggestions] = React.useState<AllianceSuggestion[]>([]);
  const [savedSnapshot, setSavedSnapshot] = React.useState("");

  const dirty = document !== null && savedSnapshot !== profileSnapshotKey(document, environment);
  const stats = document ? documentStats(document) : null;
  const saveLabel = version === 1 ? "保存首版（v2）" : `保存修改（v${version + 1}）`;

  const load = React.useCallback(
    (silent = false) => {
      if (!silent) setLoading(true);
      Promise.all([
        api.allianceProfile(systemId),
        api.allianceSuggestions({ systemId }).catch(() => [] as AllianceSuggestion[]),
      ])
        .then(([p, sugg]) => {
          const env = p.profile.environment ?? "";
          setDocument(p.profile.document);
          setEnvironment(env);
          setVersion(p.profile.version);
          setSuggestions(sugg);
          setSavedSnapshot(profileSnapshotKey(p.profile.document, env));
          // 静默刷新保留用户当前所在阶段，避免接受建议后跳回第一个阶段。
          if (!silent) setPhase(String(startAtIncomplete ? firstIncompletePhase(p.profile.document) : 1));
        })
        .catch((err: Error) => toast.error(`加载画像失败：${err.message}`))
        .finally(() => {
          if (!silent) setLoading(false);
        });
    },
    [systemId, startAtIncomplete],
  );

  React.useEffect(() => {
    if (open) load();
  }, [open, load]);

  function updateField(sectionId: string, fieldId: string, patch: Partial<AllianceProfileField>) {
    setDocument((doc) => {
      if (!doc) return doc;
      return {
        ...doc,
        sections: doc.sections.map((s) =>
          s.id === sectionId ? { ...s, fields: s.fields.map((f) => (f.id === fieldId ? { ...f, ...patch } : f)) } : s,
        ),
      };
    });
  }

  function addSection(from: AllianceProfileSection) {
    const title = window.prompt("新分组名称（例如：代理身份传递）");
    if (!title?.trim()) return;
    const id = `G-${Date.now().toString(36)}`;
    setDocument((doc) =>
      doc ? { ...doc, sections: [...doc.sections, { id, title: title.trim(), phase: from.phase, fields: [] }] } : doc,
    );
  }

  function addField(sectionId: string) {
    const title = window.prompt("新字段名称");
    if (!title?.trim()) return;
    const id = `C-${Date.now().toString(36)}`;
    const field: AllianceProfileField = {
      id,
      title: title.trim(),
      value: "",
      status: "unknown",
      source: "manual",
    };
    setDocument((doc) =>
      doc
        ? {
            ...doc,
            sections: doc.sections.map((s) => (s.id === sectionId ? { ...s, fields: [...s.fields, field] } : s)),
          }
        : doc,
    );
  }

  async function save() {
    if (!document || !dirty) return;
    const isInitial = version === 1;
    setSaving(true);
    try {
      const r = await api.saveAllianceProfile(systemId, {
        expected_version: version,
        environment,
        document,
      });
      const env = r.profile.environment ?? environment;
      setVersion(r.profile.version);
      setDocument(r.profile.document);
      setEnvironment(env);
      setSavedSnapshot(profileSnapshotKey(r.profile.document, env));
      toast.success(
        isInitial ? `首版已保存（v${r.profile.version}），后续可以继续补充` : `画像已保存（v${r.profile.version}）`,
      );
      onSaved?.();
    } catch (err) {
      const msg = (err as Error).message;
      toast.error(msg.includes("冲突") ? "画像版本冲突：已被他人更新，请点「重新读取」后再保存" : msg);
    } finally {
      setSaving(false);
    }
  }

  function openHistory() {
    setHistoryOpen(true);
    api
      .allianceProfileHistory(systemId)
      .then(setHistory)
      .catch((err: Error) => toast.error(`加载历史失败：${err.message}`));
  }

  const pending = suggestions.filter((s) => s.state === "pending");

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] overflow-y-auto sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>
            联盟画像 · {systemName} <span className="ml-2 font-normal text-muted-foreground text-sm">v{version}</span>
          </DialogTitle>
          <DialogDescription>
            结构化背景资料：三阶段字段、字段确认状态与版本历史。保存后新建任务才会绑定新版本，旧任务快照不变。
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="py-10 text-center text-muted-foreground text-sm">加载画像中…</div>
        ) : document ? (
          <div className="grid gap-4">
            <Card className="gap-1 border-primary/30 bg-primary/5 p-3 text-sm">
              <div className="font-medium">先保存一版，再慢慢补</div>
              <div className="text-muted-foreground text-xs">
                建议先完成「首轮接入」并保存首版；深入测试、持续完善可以以后补。不确定的字段请留空，需要说明时写「待确认：…」。
              </div>
              {stats ? (
                <div className="text-muted-foreground text-xs">
                  有效已填 {stats.filled}/{stats.total} · 待确认说明 {stats.pending} · 未填 {stats.empty}
                </div>
              ) : null}
            </Card>

            <div className="grid gap-3 md:grid-cols-[1fr_auto_auto] md:items-end">
              <div className="grid gap-1.5">
                <Label className="text-sm">环境</Label>
                <Input
                  value={environment}
                  onChange={(e) => setEnvironment(e.target.value)}
                  placeholder="生产 / 预生产 / 测试（建议一系统一环境）"
                />
              </div>
              <Button variant="outline" onClick={() => load()} disabled={saving}>
                <RefreshCwIcon /> 重新读取
              </Button>
              <Button variant="outline" onClick={openHistory}>
                <HistoryIcon /> 版本历史
              </Button>
            </div>

            <Tabs value={phase} onValueChange={setPhase}>
              <TabsList className="w-fit">
                {document.sections
                  .filter((s) => [1, 2, 3].includes(s.phase))
                  .filter((s, i, arr) => arr.findIndex((x) => x.phase === s.phase) === i)
                  .map((s) => (
                    <TabsTrigger key={s.phase} value={String(s.phase)}>
                      {s.phase}. {s.title}
                      {stats?.byPhase[s.phase] ? (
                        <span className="ml-1 text-[11px] text-muted-foreground">
                          {stats.byPhase[s.phase].filled}/{stats.byPhase[s.phase].total}
                        </span>
                      ) : null}
                    </TabsTrigger>
                  ))}
              </TabsList>
              {[1, 2, 3].map((p) => (
                <TabsContent key={p} value={String(p)} className="mt-3 grid gap-3">
                  {document.sections
                    .filter((s) => s.phase === p)
                    .map((section) => (
                      <Card key={section.id} className="grid gap-3 p-3">
                        <div className="flex items-center justify-between gap-2">
                          <div className="font-medium text-sm">
                            {section.title} <span className="text-muted-foreground text-xs">{section.id}</span>
                          </div>
                          <Button size="sm" variant="ghost" onClick={() => addField(section.id)}>
                            <PlusIcon /> 字段
                          </Button>
                        </div>
                        {section.fields.map((f) => (
                          <div key={f.id} className="grid gap-1.5 rounded-md border p-2">
                            <div className="flex flex-wrap items-center justify-between gap-2">
                              <span className="font-medium text-xs">
                                {f.title} <span className="text-muted-foreground">{f.id}</span>
                              </span>
                              <Select
                                value={f.status}
                                onValueChange={(v) => updateField(section.id, f.id, { status: v })}
                              >
                                <SelectTrigger className="h-7 w-36 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {STATUS_ORDER.map((s) => (
                                    <SelectItem key={s} value={s}>
                                      {STATUS_LABELS[s]}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </div>
                            {f.guidance ? <div className="text-muted-foreground text-xs">{f.guidance}</div> : null}
                            <Textarea
                              value={f.value}
                              onChange={(e) => updateField(section.id, f.id, { value: e.target.value })}
                              rows={2}
                              placeholder="未知请留空；需要说明时以「待确认：」开头"
                            />
                            {fieldBucket(f) === "pending" ? (
                              <div className="text-amber-600 text-[11px]">待确认说明，不计入已填。</div>
                            ) : null}
                            {f.status === "not_applicable" && !f.value.trim() ? (
                              <div className="text-destructive text-[11px]">不适用需要写原因，当前不计入已填。</div>
                            ) : null}
                            {f.source ? (
                              <div className="text-muted-foreground text-[11px]">来源：{f.source}</div>
                            ) : null}
                          </div>
                        ))}
                        {section.fields.length === 0 ? (
                          <div className="text-muted-foreground text-xs">暂无字段，点右上「字段」添加。</div>
                        ) : null}
                        <div>
                          <Button size="sm" variant="outline" onClick={() => addSection(section)}>
                            <PlusIcon /> 添加分组
                          </Button>
                        </div>
                      </Card>
                    ))}
                </TabsContent>
              ))}
            </Tabs>

            {pending.length > 0 ? (
              <div className="grid gap-2">
                <div className="flex items-center gap-2 font-medium text-sm">
                  <LightbulbIcon className="size-4" /> 待审核建议（{pending.length}）
                </div>
                {pending.map((s) => (
                  <AllianceSuggestionCard
                    key={s.id}
                    suggestion={s}
                    currentVersion={version}
                    onReviewed={() => {
                      load(true);
                      onSaved?.();
                    }}
                  />
                ))}
              </div>
            ) : null}
          </div>
        ) : (
          <div className="py-10 text-center text-muted-foreground text-sm">画像加载失败</div>
        )}

        <DialogFooter>
          <div className="mr-auto text-muted-foreground text-xs">{dirty ? "有未保存修改" : "没有修改"}</div>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            关闭
          </Button>
          <Button onClick={save} disabled={saving || !document || !dirty}>
            {saving ? "保存中…" : saveLabel}
          </Button>
        </DialogFooter>
      </DialogContent>

      <Dialog open={historyOpen} onOpenChange={setHistoryOpen}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>版本历史</DialogTitle>
            <DialogDescription>每次保存或接受建议都会形成新版本；旧版本只读。</DialogDescription>
          </DialogHeader>
          <div className="max-h-[50vh] overflow-auto">
            {history.map((r) => (
              <div key={r.version} className="flex items-center justify-between border-b py-2 text-sm last:border-0">
                <span className="font-medium">v{r.version}</span>
                <span className="truncate text-muted-foreground text-xs" title={r.source}>
                  {r.source || "manual"}
                </span>
                <span className="text-muted-foreground text-xs">{r.created_at?.slice(0, 19)}</span>
              </div>
            ))}
            {history.length === 0 ? (
              <div className="py-6 text-center text-muted-foreground text-sm">暂无历史</div>
            ) : null}
          </div>
        </DialogContent>
      </Dialog>
    </Dialog>
  );
}

// AllianceSuggestionCard reviews one candidate. The reviewer may edit the value
// before accepting; acceptance replaces the whole field (server-side semantics).
export function AllianceSuggestionCard({
  suggestion,
  currentVersion,
  onReviewed,
}: {
  suggestion: AllianceSuggestion;
  currentVersion: number;
  onReviewed: () => void;
}) {
  const [value, setValue] = React.useState(suggestion.suggestion.proposed_value);
  const [busy, setBusy] = React.useState(false);
  const [evidenceOpen, setEvidenceOpen] = React.useState(false);
  const [evidence, setEvidence] = React.useState<{ id: number; kind: string; payload: unknown }[]>([]);

  async function review(decision: "accepted" | "rejected") {
    setBusy(true);
    try {
      await api.reviewAllianceSuggestion(suggestion.id, {
        decision,
        expected_version: currentVersion,
        value: decision === "accepted" ? value : undefined,
      });
      toast.success(decision === "accepted" ? "已接受并写入新版本" : "已拒绝");
      onReviewed();
    } catch (err) {
      const msg = (err as Error).message;
      toast.error(msg.includes("冲突") ? "版本冲突：画像已被更新，请刷新后重试" : msg);
    } finally {
      setBusy(false);
    }
  }

  function showEvidence() {
    setEvidenceOpen(true);
    api
      .allianceSuggestionEvidence(suggestion.id)
      .then((items) => setEvidence(items as { id: number; kind: string; payload: unknown }[]))
      .catch((err: Error) => toast.error(`加载证据失败：${err.message}`));
  }

  return (
    <Card className="grid gap-2 p-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <Badge variant="secondary">
          {suggestion.suggestion.field_title}（{suggestion.suggestion.field_id}）
        </Badge>
        <span className="text-muted-foreground">task #{suggestion.task_id}</span>
        <span className="text-muted-foreground">system #{suggestion.system_id}</span>
        {suggestion.state !== "pending" ? <Badge variant="outline">{suggestion.state}</Badge> : null}
      </div>
      <div className="text-muted-foreground text-xs">理由：{suggestion.suggestion.reason || "（未提供）"}</div>
      <Textarea value={value} onChange={(e) => setValue(e.target.value)} rows={3} />
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" onClick={() => review("accepted")} disabled={busy || suggestion.state !== "pending"}>
          确认并更新画像
        </Button>
        <Button
          size="sm"
          variant="outline"
          onClick={() => review("rejected")}
          disabled={busy || suggestion.state !== "pending"}
        >
          拒绝
        </Button>
        <Button size="sm" variant="ghost" onClick={showEvidence}>
          查看证据（{suggestion.suggestion.evidence_ids?.length ?? 0}）
        </Button>
      </div>
      <Dialog open={evidenceOpen} onOpenChange={setEvidenceOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>建议证据</DialogTitle>
            <DialogDescription>来自本任务已确认的事实/发现节点。</DialogDescription>
          </DialogHeader>
          <div className="max-h-[55vh] overflow-auto">
            {evidence.map((e) => (
              <pre key={e.id} className="mb-2 max-h-48 overflow-auto whitespace-pre-wrap rounded bg-muted p-2 text-xs">
                #{e.id} [{e.kind}] {JSON.stringify(e.payload, null, 2)}
              </pre>
            ))}
            {evidence.length === 0 ? <div className="text-muted-foreground text-sm">暂无证据</div> : null}
          </div>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

// AllianceProfileDocumentView renders a frozen snapshot read-only (task tab).
export function AllianceProfileDocumentView({ document: doc }: { document: AllianceProfileDocument }) {
  return (
    <div className="grid gap-3">
      {[1, 2, 3].map((p) => (
        <div key={p} className="grid gap-2">
          {doc.sections
            .filter((s) => s.phase === p)
            .map((section) => (
              <Card key={section.id} className="grid gap-1 p-3">
                <div className="font-medium text-sm">{section.title}</div>
                {section.fields
                  .filter((f) => f.value?.trim() || f.status === "not_applicable")
                  .map((f) => (
                    <div key={f.id} className="text-xs">
                      <span className="text-muted-foreground">
                        {f.title}（{statusLabel(f.status)}）：
                      </span>
                      <span className="whitespace-pre-wrap">{f.value || "—"}</span>
                    </div>
                  ))}
                {section.fields.every((f) => !f.value?.trim() && f.status !== "not_applicable") ? (
                  <div className="text-muted-foreground text-xs">（本阶段暂无已填内容）</div>
                ) : null}
              </Card>
            ))}
        </div>
      ))}
    </div>
  );
}

// AllianceReadingBanner renders the soft-reading state on a task.
export function AllianceReadingBanner({
  reading,
  onRetry,
  busy,
}: {
  reading: AllianceTaskReading | null;
  onRetry: () => void;
  busy?: boolean;
}) {
  const [open, setOpen] = React.useState(false);
  if (!reading) return null;
  const state = reading.state;
  const tone =
    state === "failed"
      ? "border-amber-500/40 bg-amber-500/10"
      : state === "ready"
        ? "border-emerald-500/30 bg-emerald-500/10"
        : "border-border bg-muted/40";
  const label =
    state === "ready"
      ? "画像阅读完成（软提示，须用测试证据核实）"
      : state === "failed"
        ? `画像阅读失败，任务继续执行：${reading.error || "未知错误"}`
        : state === "reading"
          ? "正在阅读画像…"
          : state === "skipped"
            ? "未执行画像阅读（无可用模型）"
            : "等待阅读";
  return (
    <Card className={`grid gap-1 border p-3 ${tone}`}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium text-sm">{label}</span>
        <div className="ml-auto flex items-center gap-2">
          {reading.result ? (
            <Button size="sm" variant="ghost" onClick={() => setOpen((v) => !v)}>
              {open ? "收起摘要" : "查看摘要"}
            </Button>
          ) : null}
          {state !== "ready" && state !== "reading" ? (
            <Button size="sm" variant="outline" onClick={onRetry} disabled={busy}>
              {busy ? "重试中…" : "重新阅读"}
            </Button>
          ) : null}
        </div>
      </div>
      {open && reading.result ? (
        <pre className="max-h-60 overflow-auto whitespace-pre-wrap rounded bg-background/60 p-2 text-xs">
          {reading.result}
        </pre>
      ) : null}
    </Card>
  );
}
