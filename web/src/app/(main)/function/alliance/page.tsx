"use client";

// 联盟系统（fork-only: feat/alliance-system，城商行联盟定制）
//
// 台账 + 统计两个 tab。台账里每条记录 = 一个业务系统及其安全描述，
// 新建任务时在任务表单里勾选条目即可把入口注册成资产、把简报写进任务描述。

import * as React from "react";

import {
  BarChart3Icon,
  FileTextIcon,
  LightbulbIcon,
  PencilIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
} from "lucide-react";
import { toast } from "sonner";

import { AllianceProfileDialog, AllianceSuggestionCard } from "@/components/alliance-profile";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type {
  AllianceEndpoint,
  AllianceStats,
  AllianceSuggestion,
  AllianceSystem,
  AllianceSystemInput,
} from "@/lib/types";

const PLATFORM_SUGGESTIONS = ["Web", "小程序", "APP", "公众号", "混合"];

const emptyInput: AllianceSystemInput = {
  name: "",
  url: "",
  owner_unit: "",
  platform: "",
  framework: "",
  crypto: "",
  business: "",
  security_note: "",
  tags: [],
  status: "active",
};

// syncNote splits the stored security_note back into the two form fields:
// 第一行是「中间件/组件：…」，其余行都是「其他」。
function splitNote(note: string): { middleware: string; other: string } {
  const lines = (note ?? "").split("\n");
  const middleware = lines[0]?.startsWith("中间件/组件：") ? lines[0].slice("中间件/组件：".length) : "";
  const other = (middleware ? lines.slice(1) : lines).join("\n").trim();
  return { middleware: middleware.trim(), other };
}

function fmtTime(ts: string): string {
  if (!ts) return "";
  const t = new Date(ts);
  if (Number.isNaN(t.getTime())) return ts.slice(0, 16);
  return t.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

// 127.0.0.1/localhost 在容器里指容器自身，不是宿主机；任务 worker 也在容器中。
function isLoopbackAddress(value: string): boolean {
  const raw = value.trim();
  if (!raw) return false;
  try {
    const host = new URL(raw).hostname.toLowerCase();
    return host === "127.0.0.1" || host === "localhost" || host === "::1" || host === "[::1]";
  } catch {
    return /^(127\.0\.0\.1|localhost)(?::|\/|$)/i.test(raw);
  }
}

export default function AlliancePage() {
  const [tab, setTab] = React.useState("ledger");
  const [systems, setSystems] = React.useState<AllianceSystem[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [q, setQ] = React.useState("");
  const [qApplied, setQApplied] = React.useState("");
  const [platformFilter, setPlatformFilter] = React.useState("");
  const [tagFilter, setTagFilter] = React.useState("");
  const [reloadTick, setReloadTick] = React.useState(0);

  const [stats, setStats] = React.useState<AllianceStats | null>(null);

  // 表单对话框
  const [dialogOpen, setDialogOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<AllianceSystem | null>(null);
  const [form, setForm] = React.useState<AllianceSystemInput>(emptyInput);
  const [middleware, setMiddleware] = React.useState("");
  const [other, setOther] = React.useState("");
  const [tagsText, setTagsText] = React.useState("");
  const [endpoints, setEndpoints] = React.useState<AllianceEndpoint[]>([]);
  const [newEndpoint, setNewEndpoint] = React.useState("");
  const [saving, setSaving] = React.useState(false);

  const [pendingDelete, setPendingDelete] = React.useState<AllianceSystem | null>(null);
  const [profileSystem, setProfileSystem] = React.useState<{ id: number; name: string } | null>(null);
  const [profileStartAtIncomplete, setProfileStartAtIncomplete] = React.useState(false);
  const [suggestions, setSuggestions] = React.useState<AllianceSuggestion[]>([]);
  const [suggestVersions, setSuggestVersions] = React.useState<Record<number, number>>({});

  const loadSuggestions = React.useCallback(() => {
    api
      .allianceSuggestions()
      .then(async (items) => {
        setSuggestions(items);
        const ids = Array.from(new Set(items.map((s) => s.system_id)));
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
        setSuggestVersions(Object.fromEntries(entries));
      })
      .catch((err: Error) => toast.error(`加载画像建议失败：${err.message}`));
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 手动刷新经 reloadTick 触发，进入页签时重拉。
  React.useEffect(() => {
    if (tab === "suggestions") loadSuggestions();
  }, [tab, loadSuggestions, reloadTick]);

  React.useEffect(() => {
    const t = setTimeout(() => setQApplied(q.trim()), 300);
    return () => clearTimeout(t);
  }, [q]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 手动刷新经 reloadTick 触发重新拉取。
  React.useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api
      .allianceSystems(qApplied, platformFilter, tagFilter)
      .then((items) => {
        if (!cancelled) setSystems(items);
      })
      .catch((err: Error) => {
        if (!cancelled) toast.error(`加载联盟台账失败：${err.message}`);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [qApplied, platformFilter, tagFilter, reloadTick]);

  const loadStats = React.useCallback(() => {
    api
      .allianceStats()
      .then(setStats)
      .catch((err: Error) => toast.error(`加载统计失败：${err.message}`));
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 手动刷新经 reloadTick 触发，统计与台账一起重算。
  React.useEffect(() => {
    if (tab === "stats") loadStats();
  }, [tab, loadStats, reloadTick]);

  function openCreate() {
    setEditing(null);
    setForm(emptyInput);
    setMiddleware("");
    setOther("");
    setTagsText("");
    setEndpoints([]);
    setNewEndpoint("");
    setDialogOpen(true);
  }

  function openEdit(sys: AllianceSystem) {
    setEditing(sys);
    setForm({
      name: sys.name,
      url: sys.url,
      owner_unit: sys.owner_unit,
      platform: sys.platform,
      framework: sys.framework,
      crypto: sys.crypto,
      business: sys.business,
      security_note: sys.security_note,
      tags: sys.tags ?? [],
      status: sys.status || "active",
    });
    const parts = splitNote(sys.security_note);
    setMiddleware(parts.middleware);
    setOther(parts.other);
    setTagsText((sys.tags ?? []).join(", "));
    setEndpoints([]);
    setNewEndpoint("");
    setDialogOpen(true);
    api
      .allianceSystem(sys.id)
      .then((r) => setEndpoints(r.endpoints ?? []))
      .catch(() => toast.error("读取入口列表失败"));
  }

  async function save() {
    const securityNote = [middleware.trim() ? `中间件/组件：${middleware.trim()}` : "", other.trim()]
      .filter(Boolean)
      .join("\n");
    const input: AllianceSystemInput = {
      ...form,
      security_note: securityNote,
      tags: tagsText
        .split(/[,，]/)
        .map((s) => s.trim())
        .filter(Boolean),
    };
    if (!input.name.trim()) {
      toast.error("系统名称不能为空");
      return;
    }
    setSaving(true);
    try {
      if (editing) {
        await api.updateAllianceSystem(editing.id, input);
        toast.success("已保存");
      } else {
        const { id } = await api.createAllianceSystem(input);
        toast.success("基本信息已保存，继续填写画像");
        setDialogOpen(false);
        setReloadTick((n) => n + 1);
        setProfileStartAtIncomplete(false);
        setProfileSystem({ id, name: input.name.trim() });
        return;
      }
      setDialogOpen(false);
      setReloadTick((n) => n + 1);
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function remove(sys: AllianceSystem) {
    try {
      await api.deleteAllianceSystem(sys.id);
      toast.success(`已删除「${sys.name}」`);
      setPendingDelete(null);
      setReloadTick((n) => n + 1);
    } catch (err) {
      toast.error((err as Error).message);
    }
  }

  async function addEndpoint() {
    if (!editing || !newEndpoint.trim()) return;
    try {
      await api.addAllianceEndpoint(editing.id, { url: newEndpoint.trim() });
      setNewEndpoint("");
      const r = await api.allianceSystem(editing.id);
      setEndpoints(r.endpoints ?? []);
      toast.success("入口已添加");
      setReloadTick((n) => n + 1);
    } catch (err) {
      toast.error((err as Error).message);
    }
  }

  async function removeEndpoint(ep: AllianceEndpoint) {
    if (!editing) return;
    try {
      await api.deleteAllianceEndpoint(editing.id, ep.id);
      setEndpoints((list) => list.filter((x) => x.id !== ep.id));
      setReloadTick((n) => n + 1);
    } catch (err) {
      toast.error((err as Error).message);
    }
  }

  return (
    <div className="flex h-full flex-col gap-4 p-5">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="font-semibold text-xl">联盟画像</h1>
          <p className="text-muted-foreground text-sm">
            城商行联盟业务系统画像台账：登记系统、入口与结构化背景；新建任务勾选后绑定固定快照，测试发现可反哺画像。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => setReloadTick((n) => n + 1)} disabled={loading}>
            <RefreshCwIcon className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
          <Button size="sm" onClick={openCreate}>
            <PlusIcon />
            新建画像
          </Button>
        </div>
      </div>

      <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
        <TabsList className="w-fit">
          <TabsTrigger value="ledger">台账</TabsTrigger>
          <TabsTrigger value="stats">
            <BarChart3Icon className="mr-1 size-4" />
            统计
          </TabsTrigger>
          <TabsTrigger value="suggestions">
            <LightbulbIcon className="mr-1 size-4" />
            画像建议
            {suggestions.filter((s) => s.state === "pending").length > 0 ? (
              <Badge variant="secondary" className="ml-1">
                {suggestions.filter((s) => s.state === "pending").length}
              </Badge>
            ) : null}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="ledger" className="mt-4 min-h-0 flex-1">
          <Card className="flex h-full min-h-0 flex-col overflow-hidden">
            <div className="flex flex-wrap items-center gap-2 border-b p-3">
              <div className="relative">
                <SearchIcon className="absolute top-1/2 left-2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                  placeholder="搜索名称 / URL / 框架 / 业务 / 单位"
                  className="w-72 pl-8"
                />
              </div>
              <Input
                value={platformFilter}
                onChange={(e) => setPlatformFilter(e.target.value)}
                placeholder="形态筛选（如 Web）"
                className="w-40"
              />
              <Input
                value={tagFilter}
                onChange={(e) => setTagFilter(e.target.value)}
                placeholder="标签筛选"
                className="w-40"
              />
              <span className="ml-auto text-muted-foreground text-sm">{systems.length} 条</span>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden">
              <Table className="table-fixed" containerClassName="overflow-x-hidden">
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-[4%] whitespace-normal">ID</TableHead>
                    <TableHead className="w-[18%] whitespace-normal">系统名称</TableHead>
                    <TableHead className="hidden w-[6%] whitespace-normal md:table-cell">形态</TableHead>
                    <TableHead className="w-[22%] whitespace-normal">入口</TableHead>
                    <TableHead className="hidden w-[13%] whitespace-normal lg:table-cell">技术框架</TableHead>
                    <TableHead className="hidden w-[12%] whitespace-normal xl:table-cell">登录/加解密</TableHead>
                    <TableHead className="w-[6%] whitespace-normal">状态</TableHead>
                    <TableHead className="hidden w-[8%] whitespace-normal md:table-cell">更新时间</TableHead>
                    <TableHead className="w-[11%] whitespace-normal">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {systems.map((sys) => (
                    <TableRow key={sys.id}>
                      <TableCell className="text-muted-foreground">{sys.id}</TableCell>
                      <TableCell className="whitespace-normal">
                        <div className="font-medium break-words">{sys.name}</div>
                        {sys.tags?.length ? (
                          <div className="mt-1 flex flex-wrap gap-1">
                            {sys.tags.map((t) => (
                              <Badge key={t} variant="secondary" className="font-normal">
                                {t}
                              </Badge>
                            ))}
                          </div>
                        ) : null}
                      </TableCell>
                      <TableCell className="hidden md:table-cell">{sys.platform || "-"}</TableCell>
                      <TableCell className="whitespace-normal">
                        <div className="max-w-72 truncate" title={sys.url}>
                          {sys.url || "-"}
                        </div>
                        <div className="text-muted-foreground text-xs">{sys.endpoint_count} 个入口</div>
                      </TableCell>
                      <TableCell className="hidden max-w-56 truncate lg:table-cell" title={sys.framework}>
                        {sys.framework || "-"}
                      </TableCell>
                      <TableCell className="hidden max-w-56 truncate xl:table-cell" title={sys.crypto}>
                        {sys.crypto || "-"}
                      </TableCell>
                      <TableCell>
                        {sys.status === "archived" ? <Badge variant="outline">已归档</Badge> : <Badge>在用</Badge>}
                      </TableCell>
                      <TableCell className="hidden text-muted-foreground text-xs md:table-cell">
                        {fmtTime(sys.updated_at)}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap items-center gap-1">
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => {
                              setProfileStartAtIncomplete(false);
                              setProfileSystem(sys);
                            }}
                            title="查看联盟画像"
                          >
                            <FileTextIcon className="size-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 px-2 text-xs"
                            onClick={() => {
                              setProfileStartAtIncomplete(true);
                              setProfileSystem(sys);
                            }}
                            title="从第一个未完成阶段继续补充"
                          >
                            继续补充
                          </Button>
                          <Button variant="ghost" size="icon" onClick={() => openEdit(sys)} title="编辑台账字段">
                            <PencilIcon className="size-4" />
                          </Button>
                          <Button variant="ghost" size="icon" onClick={() => setPendingDelete(sys)} title="删除">
                            <Trash2Icon className="size-4" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                  {!loading && systems.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={9} className="h-32 text-center text-muted-foreground">
                        暂无联盟系统，点右上角「新建系统」开始登记。
                      </TableCell>
                    </TableRow>
                  ) : null}
                </TableBody>
              </Table>
            </div>
          </Card>
        </TabsContent>

        <TabsContent value="stats" className="mt-4 min-h-0 flex-1 overflow-auto">
          <div className="grid gap-3">
            <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
              <StatCard label="系统数" value={stats?.systems} />
              <StatCard label="入口数" value={stats?.endpoints} />
              <StatCard label="已关联任务" value={stats?.tasks} />
              <StatCard label="已纳入任务的系统" value={stats?.covered} />
              <StatCard
                label="严重/高危"
                value={stats ? `${stats.findings.critical}/${stats.findings.high}` : undefined}
              />
              <StatCard
                label="中危/低危"
                value={stats ? `${stats.findings.medium}/${stats.findings.low}` : undefined}
              />
            </div>
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
              <BucketCard title="系统形态分布" buckets={stats?.platforms} />
              <BucketCard title="技术框架分布" buckets={stats?.frameworks} />
              <BucketCard title="登录/加解密分布" buckets={stats?.cryptos} />
              <BucketCard title="标签 TOP" buckets={stats?.tags} />
            </div>
          </div>
        </TabsContent>

        <TabsContent value="suggestions" className="mt-4 min-h-0 flex-1 overflow-auto">
          <div className="grid gap-3">
            {suggestions.filter((s) => s.state === "pending").length === 0 ? (
              <Card className="p-6 text-center text-muted-foreground text-sm">
                暂无待审核建议。任务完成/失败/超时并收尾后，平台会从本任务已确认的事实与发现里整理候选。
              </Card>
            ) : null}
            {suggestions
              .filter((s) => s.state === "pending")
              .map((s) => (
                <AllianceSuggestionCard
                  key={s.id}
                  suggestion={s}
                  currentVersion={suggestVersions[s.system_id] ?? 0}
                  onReviewed={() => setReloadTick((n) => n + 1)}
                />
              ))}
            {suggestions.filter((s) => s.state !== "pending").length > 0 ? (
              <Card className="grid gap-2 p-3">
                <div className="font-medium text-sm">
                  已处理（{suggestions.filter((s) => s.state !== "pending").length}）
                </div>
                {suggestions
                  .filter((s) => s.state !== "pending")
                  .slice(0, 50)
                  .map((s) => (
                    <div
                      key={s.id}
                      className="flex flex-wrap items-center gap-2 border-t py-1.5 text-xs first:border-0"
                    >
                      <Badge variant="outline">{s.state}</Badge>
                      <span className="text-muted-foreground">
                        {s.suggestion.field_title}（{s.suggestion.field_id}） · task #{s.task_id} · system #
                        {s.system_id}
                      </span>
                      {s.accepted_version ? (
                        <span className="text-muted-foreground">→ v{s.accepted_version}</span>
                      ) : null}
                    </div>
                  ))}
              </Card>
            ) : null}
          </div>
        </TabsContent>
      </Tabs>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{editing ? `编辑台账「${editing.name}」` : "新建联盟画像 · 第 1 步：基本信息"}</DialogTitle>
            <DialogDescription>
              {editing
                ? "这里改的是台账字段（列表、统计与任务简报用）；结构化画像点下方「编辑画像」。"
                : "先登记系统名称与入口；保存后进入第 2 步，按三阶段填写画像字段。带 * 的为必填。"}
            </DialogDescription>
          </DialogHeader>

          <div className="grid gap-4">
            <div className="grid gap-4 md:grid-cols-2">
              <Field label="系统名称 *">
                <Input
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="例：信贷管理系统"
                />
              </Field>
              <Field label="访问地址（主入口）">
                <Input
                  value={form.url}
                  onChange={(e) => setForm({ ...form, url: e.target.value })}
                  placeholder="例：https://x.example.com（有测试环境请标注）"
                />
                {isLoopbackAddress(form.url) ? (
                  <div className="text-amber-600 text-xs">
                    如果 SERIES 跑在 Docker 容器里，127.0.0.1 / localhost 指容器自身，任务 worker
                    访问不到宿主机上的靶场。请改用容器可达地址（例如 host.docker.internal）或在宿主机做端口转发。
                  </div>
                ) : null}
              </Field>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <Field label="系统形态">
                <Input
                  value={form.platform}
                  onChange={(e) => setForm({ ...form, platform: e.target.value })}
                  placeholder="Web / 小程序 / APP / 公众号 / 混合"
                />
                <div className="mt-1 flex flex-wrap gap-1">
                  {PLATFORM_SUGGESTIONS.map((p) => (
                    <Button
                      key={p}
                      type="button"
                      variant="outline"
                      size="sm"
                      className="h-6 px-2 text-xs"
                      onClick={() => setForm({ ...form, platform: form.platform ? `${form.platform},${p}` : p })}
                    >
                      {p}
                    </Button>
                  ))}
                </div>
              </Field>
              <Field label="所属单位（选填）">
                <Input
                  value={form.owner_unit}
                  onChange={(e) => setForm({ ...form, owner_unit: e.target.value })}
                  placeholder="例：某某银行"
                />
              </Field>
            </div>

            <Field label="业务功能 *">
              <Textarea
                value={form.business}
                onChange={(e) => setForm({ ...form, business: e.target.value })}
                placeholder="一两句话：给谁用、干什么。例：客户经理提交贷款申请，支行/总行分级审批，对接核心系统放款"
                rows={2}
              />
            </Field>

            <div className="grid gap-4 md:grid-cols-2">
              <Field label="技术框架">
                <Input
                  value={form.framework}
                  onChange={(e) => setForm({ ...form, framework: e.target.value })}
                  placeholder="例：Java Spring Boot + Vue（JeecgBoot）；不确定写「不清楚」"
                />
              </Field>
              <Field label="登录与加解密">
                <Input
                  value={form.crypto}
                  onChange={(e) => setForm({ ...form, crypto: e.target.value })}
                  placeholder="例：账号密码登录；前端 RSA 加密口令；接口 token 放 header"
                />
              </Field>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <Field label="中间件/组件">
                <Input
                  value={middleware}
                  onChange={(e) => setMiddleware(e.target.value)}
                  placeholder="例：Nginx + MySQL + Redis + Nacos"
                />
              </Field>
              <Field label="标签（逗号分隔）">
                <Input
                  value={tagsText}
                  onChange={(e) => setTagsText(e.target.value)}
                  placeholder="例：核心业务, 已上线"
                />
              </Field>
            </div>

            <Field label="其他补充（测试注意事项 / 历史问题 / 对接人）">
              <Textarea
                value={other}
                onChange={(e) => setOther(e.target.value)}
                placeholder="例：生产数据，禁止删除/修改；有 WAF；测试账号联系 张三"
                rows={3}
              />
            </Field>

            <div className="flex items-end gap-4">
              <Field label="状态">
                <Select value={form.status} onValueChange={(v) => setForm({ ...form, status: v })}>
                  <SelectTrigger className="w-40">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="active">在用</SelectItem>
                    <SelectItem value="archived">已归档</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            </div>

            {editing ? (
              <div className="grid gap-2 rounded-md border p-3">
                <div className="font-medium text-sm">入口列表（新建任务勾选该条目时会把这些入口登记为资产）</div>
                <div className="grid gap-1">
                  {endpoints.map((ep) => (
                    <div key={ep.id} className="flex items-center justify-between gap-2 text-sm">
                      <span className="truncate" title={ep.url}>
                        {ep.url}
                        {ep.asset_id ? (
                          <span className="ml-2 text-muted-foreground text-xs">asset #{ep.asset_id}</span>
                        ) : null}
                      </span>
                      <Button variant="ghost" size="icon" onClick={() => removeEndpoint(ep)} title="删除入口">
                        <Trash2Icon className="size-4" />
                      </Button>
                    </div>
                  ))}
                  {endpoints.length === 0 ? <span className="text-muted-foreground text-sm">暂无入口</span> : null}
                </div>
                <div className="flex items-center gap-2">
                  <Input
                    value={newEndpoint}
                    onChange={(e) => setNewEndpoint(e.target.value)}
                    placeholder="新增入口 URL，例：https://x.example.com:8443"
                  />
                  <Button type="button" variant="outline" onClick={addEndpoint}>
                    添加
                  </Button>
                </div>
              </div>
            ) : null}
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)} disabled={saving}>
              取消
            </Button>
            {editing ? (
              <Button
                variant="secondary"
                disabled={saving}
                onClick={() => {
                  setDialogOpen(false);
                  setProfileSystem({ id: editing.id, name: editing.name });
                }}
              >
                编辑画像
              </Button>
            ) : null}
            <Button onClick={save} disabled={saving}>
              {saving ? "保存中…" : editing ? "保存" : "保存并填写画像"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {profileSystem ? (
        <AllianceProfileDialog
          systemId={profileSystem.id}
          systemName={profileSystem.name}
          open
          startAtIncomplete={profileStartAtIncomplete}
          onOpenChange={(open) => {
            if (!open) {
              setProfileSystem(null);
              setProfileStartAtIncomplete(false);
            }
          }}
          onSaved={() => setReloadTick((n) => n + 1)}
        />
      ) : null}

      <AlertDialog open={pendingDelete !== null} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除「{pendingDelete?.name}」？</AlertDialogTitle>
            <AlertDialogDescription>
              删除后台账条目、入口与任务关联都会移除；已注册到资产库的资产不受影响。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={() => pendingDelete && remove(pendingDelete)}>删除</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function StatCard({ label, value }: { label: string; value?: number | string }) {
  return (
    <Card className="p-3">
      <div className="text-muted-foreground text-xs">{label}</div>
      <div className="mt-1 font-semibold text-xl">{value ?? "-"}</div>
    </Card>
  );
}

function BucketCard({ title, buckets }: { title: string; buckets?: { label: string; count: number }[] }) {
  return (
    <Card className="p-3">
      <div className="font-medium text-sm">{title}</div>
      <div className="mt-2 grid gap-1">
        {buckets?.length ? (
          buckets.map((b) => (
            <div key={b.label} className="flex items-center justify-between gap-2 text-sm">
              <span className="truncate" title={b.label}>
                {b.label}
              </span>
              <span className="text-muted-foreground">{b.count}</span>
            </div>
          ))
        ) : (
          <span className="text-muted-foreground text-sm">暂无数据</span>
        )}
      </div>
    </Card>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <Label className="text-sm">{label}</Label>
      {children}
    </div>
  );
}
