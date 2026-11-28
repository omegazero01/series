"use client";

import * as React from "react";

import { FileTextIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { StatusBadge } from "@/components/status-badge";
import { api } from "@/lib/api";
import { fmtTime } from "@/lib/findings";
import type { Finding } from "@/lib/types";

type Props = {
  taskId?: string;
  onStarted?: () => void;
};

// 左侧发现列表的全局补报告入口：自动列出提交版报告为空的漏洞，允许跨页多选。
// taskId 存在时只检查该任务的发现（任务详情的内嵌发现页使用）。
export function FindingReportBackfill({ taskId, onStarted }: Props) {
  const [open, setOpen] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState("");
  const [items, setItems] = React.useState<Finding[]>([]);
  const [total, setTotal] = React.useState(0);
  const [page, setPage] = React.useState(1);
  const [selected, setSelected] = React.useState<Set<string>>(() => new Set());
  const pageSize = 50;

  const load = React.useCallback(async (nextPage: number) => {
    setLoading(true);
    setError("");
    try {
      const result = await api.missingFindingReports({
        page: nextPage,
        pageSize,
        ...(taskId ? { task: taskId } : {}),
      });
      setItems(result.items);
      setTotal(result.total);
      setPage(result.page);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [taskId]);

  React.useEffect(() => {
    if (!open) return;
    setSelected(new Set());
    void load(1);
  }, [open, load]);

  const selectable = items.filter((item): item is Finding & { finding_id: string } => !!item.finding_id);
  const selectedOnPage = selectable.filter((item) => selected.has(item.finding_id)).length;
  const allOnPage = selectable.length > 0 && selectedOnPage === selectable.length;

  function toggle(id: string, checked: boolean) {
    setSelected((current) => {
      const next = new Set(current);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  }

  async function submit() {
    const ids = Array.from(selected);
    if (!ids.length || submitting) return;
    setSubmitting(true);
    try {
      const result = await api.generateFindingReports(ids);
      const skipped = result.skipped.length;
      toast.success(
        skipped
          ? `已排入 ${result.queued} 份报告，${skipped} 条跳过`
          : `已排入 ${result.queued} 份报告，生成完成后会写入漏洞详情`,
      );
      if (skipped) toast.warning(`跳过原因：${result.skipped[0]?.reason ?? "未知"}`);
      setOpen(false);
      onStarted?.();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      <Button
        variant="outline"
        size="sm"
        title="自动检查并出具所有缺失的提交报告"
        onClick={() => setOpen(true)}
      >
        <FileTextIcon data-icon="inline-start" />
        补出报告
      </Button>

      <Dialog open={open} onOpenChange={(next) => !submitting && setOpen(next)}>
        <DialogContent className="max-h-[85dvh] sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>补出提交报告</DialogTitle>
            <DialogDescription>
              自动列出提交版报告为空的漏洞。可以跨页多选，只为你选中的漏洞排入报告撰写任务。
            </DialogDescription>
          </DialogHeader>

          <div className="flex min-h-0 flex-1 flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
              <div className="text-muted-foreground">
                共 <span className="font-medium text-foreground tabular-nums">{total}</span> 条未出具报告
                {selected.size > 0 && <span> · 已选 {selected.size} 条</span>}
              </div>
              <label className="flex items-center gap-2 text-muted-foreground text-xs">
                <Checkbox
                  checked={allOnPage ? true : selectedOnPage > 0 ? "indeterminate" : false}
                  disabled={selectable.length === 0}
                  onCheckedChange={(value) => {
                    setSelected((current) => {
                      const next = new Set(current);
                      for (const item of selectable) {
                        if (value === true) next.add(item.finding_id);
                        else next.delete(item.finding_id);
                      }
                      return next;
                    });
                  }}
                />
                全选本页
              </label>
            </div>

            {error && (
              <p className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-destructive text-sm">
                加载失败：{error}
                <Button className="ml-2" variant="link" size="sm" onClick={() => void load(page)}>
                  重试
                </Button>
              </p>
            )}

            <div className="min-h-40 flex-1 overflow-y-auto rounded-md border">
              {loading ? (
                <div className="flex min-h-40 items-center justify-center gap-2 text-muted-foreground text-sm">
                  <Spinner />
                  正在检查未出具报告的漏洞…
                </div>
              ) : items.length === 0 ? (
                <div className="flex min-h-40 items-center justify-center px-4 text-center text-muted-foreground text-sm">
                  {total === 0 && !error ? "没有缺少提交版报告的漏洞。" : "当前页没有可选择的漏洞。"}
                </div>
              ) : (
                <ul className="divide-y">
                  {items.map((item) => {
                    const id = item.finding_id;
                    return (
                      <li key={id ?? item.id} className="flex gap-3 p-3">
                        <Checkbox
                          className="mt-0.5"
                          aria-label={`选择 ${item.name || item.vulnclass}`}
                          checked={!!id && selected.has(id)}
                          disabled={!id}
                          onCheckedChange={(value) => id && toggle(id, value === true)}
                        />
                        <div className="min-w-0 flex-1">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="break-words font-medium text-sm">
                              {item.name || item.vulnclass || "未分类漏洞"}
                            </span>
                            <StatusBadge domain="severity" value={item.severity} dot />
                          </div>
                          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground text-xs">
                            <span>{item.vulnclass || "未分类"}</span>
                            {item.task_id && <span>任务 #{item.task_id}</span>}
                            {item.ts && <span>{fmtTime(item.ts)}</span>}
                          </div>
                        </div>
                      </li>
                    );
                  })}
                </ul>
              )}
            </div>

            {total > pageSize && (
              <div className="flex items-center justify-between text-xs">
                <span className="text-muted-foreground">
                  第 {page} / {Math.max(1, Math.ceil(total / pageSize))} 页
                </span>
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={page <= 1 || loading}
                    onClick={() => void load(page - 1)}
                  >
                    上一页
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={page >= Math.ceil(total / pageSize) || loading}
                    onClick={() => void load(page + 1)}
                  >
                    下一页
                  </Button>
                </div>
              </div>
            )}
          </div>

          <DialogFooter>
            <Button variant="outline" disabled={submitting} onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button disabled={selected.size === 0 || submitting} onClick={() => void submit()}>
              {submitting && <Spinner data-icon="inline-start" />}
              出具选中 {selected.size} 份报告
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
