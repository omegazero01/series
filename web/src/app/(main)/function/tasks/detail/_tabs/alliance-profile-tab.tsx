"use client";

// 任务详情「联盟画像」页签：固定快照 + 软提示阅读状态 + 反哺建议审核。

import * as React from "react";

import { RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import {
  AllianceProfileDocumentView,
  AllianceReadingBanner,
  AllianceSuggestionCard,
} from "@/components/alliance-profile";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { api } from "@/lib/api";
import type {
  AllianceFeedbackRun,
  AllianceSuggestion,
  AllianceTaskReading,
  AllianceTaskSnapshot,
} from "@/lib/types";

export function AllianceProfileTab({ taskId }: { taskId: string }) {
  const [snapshots, setSnapshots] = React.useState<AllianceTaskSnapshot[]>([]);
  const [reading, setReading] = React.useState<AllianceTaskReading | null>(null);
  const [runs, setRuns] = React.useState<AllianceFeedbackRun[]>([]);
  const [suggestions, setSuggestions] = React.useState<AllianceSuggestion[]>([]);
  const [versions, setVersions] = React.useState<Record<number, number>>({});
  const [loading, setLoading] = React.useState(false);
  const [retryingRead, setRetryingRead] = React.useState(false);
  const [expanded, setExpanded] = React.useState<Record<number, boolean>>({});

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const [profile, feedback] = await Promise.all([
        api.taskAllianceProfile(taskId),
        api.taskAllianceFeedback(taskId).catch(() => ({ runs: [], suggestions: [] })),
      ]);
      setSnapshots(profile.snapshots ?? []);
      setReading(profile.reading ?? null);
      setRuns(feedback.runs ?? []);
      setSuggestions(feedback.suggestions ?? []);
      const ids = Array.from(new Set((profile.snapshots ?? []).map((s) => s.system_id)));
      const entries = await Promise.all(
        ids.map(async (id) => {
          try {
            const r = await api.allianceProfile(id);
            return [id, r.profile.version] as const;
          } catch {
            return [id, 0] as const;
          }
        }),
      );
      setVersions(Object.fromEntries(entries));
    } catch (err) {
      toast.error(`加载联盟画像失败：${(err as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, [taskId]);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function retryRead() {
    setRetryingRead(true);
    try {
      await api.readTaskAllianceProfile(taskId);
      toast.success("画像阅读已重试");
      await load();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setRetryingRead(false);
    }
  }

  async function retryRun(run: AllianceFeedbackRun) {
    try {
      await api.retryTaskAllianceFeedback(taskId, run.id);
      toast.success("已重新排队整理");
      await load();
    } catch (err) {
      toast.error((err as Error).message);
    }
  }

  if (loading && snapshots.length === 0) {
    return <div className="py-10 text-center text-muted-foreground text-sm">加载联盟画像中…</div>;
  }

  if (snapshots.length === 0) {
    return (
      <Card className="p-6 text-center text-muted-foreground text-sm">
        本任务未绑定联盟画像。新建任务时勾选「联盟画像」即可绑定固定快照；旧任务保持原行为。
      </Card>
    );
  }

  const pending = suggestions.filter((s) => s.state === "pending");

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-muted-foreground text-sm">
          已绑定 {snapshots.length} 个联盟画像快照；正式画像之后的修改不会影响本任务。
        </div>
        <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading}>
          <RefreshCwIcon className={loading ? "animate-spin" : ""} /> 刷新
        </Button>
      </div>

      <AllianceReadingBanner reading={reading} onRetry={() => void retryRead()} busy={retryingRead} />

      {snapshots.map((s) => (
        <Card key={s.system_id} className="grid gap-2 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium text-sm">{s.system_name}</span>
            <Badge variant="secondary">v{s.profile_version}</Badge>
            {s.snapshot?.environment ? <Badge variant="outline">{s.snapshot.environment}</Badge> : null}
            <Button
              size="sm"
              variant="ghost"
              className="ml-auto"
              onClick={() => setExpanded((v) => ({ ...v, [s.system_id]: !v[s.system_id] }))}
            >
              {expanded[s.system_id] ? "收起快照" : "查看完整快照"}
            </Button>
          </div>
          {expanded[s.system_id] ? <AllianceProfileDocumentView document={s.snapshot.document} /> : null}
        </Card>
      ))}

      <Card className="grid gap-2 p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="font-medium text-sm">测试发现反哺</div>
          <div className="text-muted-foreground text-xs">
            {runs.length > 0 ? `最近整理：${runs[0].state}` : "任务结束后自动整理"}
          </div>
        </div>
        {runs.map((run) => (
          <div key={run.id} className="flex flex-wrap items-center gap-2 border-t py-1.5 text-xs first:border-0">
            <span className="text-muted-foreground">run #{run.id}</span>
            <Badge variant={run.state === "failed" ? "destructive" : "secondary"}>{run.state}</Badge>
            <span className="text-muted-foreground">{run.completed_at?.slice(0, 19)}</span>
            {run.error ? <span className="text-destructive">{run.error}</span> : null}
            {run.state === "failed" ? (
              <Button size="sm" variant="outline" className="ml-auto" onClick={() => void retryRun(run)}>
                重新整理
              </Button>
            ) : null}
          </div>
        ))}
        {runs.length === 0 ? (
          <div className="text-muted-foreground text-xs">暂无整理记录：任务进入完成/失败/超时并收尾后开始整理。</div>
        ) : null}
      </Card>

      {suggestions.length > 0 ? (
        <div className="grid gap-2">
          <div className="font-medium text-sm">
            更新建议（待审核 {pending.length} / 共 {suggestions.length}）
          </div>
          {suggestions.map((s) => (
            <AllianceSuggestionCard
              key={s.id}
              suggestion={s}
              currentVersion={versions[s.system_id] ?? 0}
              onReviewed={() => void load()}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}