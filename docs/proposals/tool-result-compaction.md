# 工具返回内容压缩：真实实例与改造方案（**已否决**）

> **状态：不采纳。** 本文用的任务 81 是极端个例。后补的全库抽样（6,000 条工具返回）显示：
> 行级重复占 2.6%、大块重复占 1.2%，逐条重复率中位 0%，重复率 >20% 的只有 3.0%。
> 真实可回收 **2~3%（约 40~50 元 / 1,597 元）**，与投入不成比例。保留本文仅作实例记录。

**样本**：任务 81（`exploration_id=81`）/ 节点 5713 / 第 1 次 Bash 调用  
**数据来源**：`activity` 表（`kind='tool_use'` / `kind='tool_result'`），原文逐字取自数据库  
**脱敏**：域名、AK/SK、租户 UUID、Cookie、Trace-Id 已掩码；状态码、响应头名、JSON 结构、中文文案保持原样

---

## 0. 结论

1. 是，**现在工具返回的全文直接写进 `messages` 的 tool_result，下一次请求把整段历史连同它一起发出去**，不做任何裁剪。
2. 这一轮 22,013 字符里，真正不重复的信息约 1,969 字符（第 2.4 节是完整替代文本），压缩 **91%**。
3. 只用三种手段：折叠完全相同的响应体（标 xN）、丢弃无判断价值的传输头与 PowerShell 进度 XML、全文落盘并把路径给模型。证据正文（状态码、指纹头、JSON 错误与数据）一条不删。

---

## 1. 现在的链路：一次工具调用怎么变成下一轮的输入

```text
模型请求 tool_use(Bash: python .\q1_items.py)
   ↓ 平台执行，拿到 stdout
平台把 stdout 全文写成 tool_result 消息（22,013 字符）
   ↓
模型下一次请求 = system + 全部历史 + 这条 22,013 字符的 tool_result
   ↓ 之后每一轮都带着它
```

计费口径（你给的价格表）：

| 部分 | 单价 | 这一次的量 |
|---|---:|---:|
| 首次出现（未缓存） | 1 元/百万 token | 约 6,100 token → 0.0061 元 |
| 之后每轮重发（缓存命中） | 0.02 元/百万 token | 剩 40 轮 ≈ 0.0049 元 |

单次不多，但全库工具返回 **3.5 亿字符**、9.9 万轮 worker 调用，累加就是 45% 的未缓存成本。

---

## 2. 实例：这一轮到底发了什么、回了什么

### 2.1 发起的东西

**平台侧调用**（worker 的一条 Bash）：

```text
cd E:\AI\series\data\tasks\81\i5713; python .\q1_items.py 2>&1
```

**脚本定义的 10 个真实 API 调用**（同一端点，只改载体做三态差分）：

| 用例 | 方法 | 路径 | 参数 / 头 / Body | 鉴权 |
|---|---|---|---|---|
| A0 匿名基线 | GET | /api/osm/v1/quota/items | 无 | 无 |
| A1 AK 无参数 | GET | 同上 | 无 | HMAC |
| A2 本租户 | GET | 同上 | 本租户 tenantId | HMAC |
| A3 他人 zetyun | GET | 同上 | 他人 tenantId | HMAC |
| A4 他人 milian | GET | 同上 | 他人 tenantId | HMAC |
| A5 不存在租户 | GET | 同上 | tenantId=00000000-0000-4000-8000-000000000001 | HMAC |
| A6 头注入 | GET | 同上 | 头 X-Tenant-Id=他人租户 | HMAC |
| A7 body 注入 | GET | 同上 | body {"tenantId":"他人租户"} | HMAC |
| A8 本租户基线 | GET | 同上 | ?aidcId=5 | HMAC |
| A9 组合 | GET | 同上 | ?aidcId=5 + 他人 tenantId | HMAC |

**展开成一条完整 curl**：

```text
curl.exe -s -k --noproxy * --max-time 25 -i -X GET \
  "https://api.<客户域名>/api/osm/v1/quota/items?tenantId=<UUID>" \
  -H "accept: application/json" \
  -H "User-Agent: curl/8.21.0" \
  -H "Authorization: <客户产品>-HMAC-SHA256 <AK>:<签名>"
```

### 2.2 目标返回的完整原文（22,013 字符，全文未删减）

```text
#< CLIXML

===== A0 匿名基线 =====

STATUS: HTTP/1.1 401 Unauthorized

Date: Sat, 10 Oct 2026 07:12:22 GMT

Content-Type: application/json; charset=utf-8

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

Server: APISIX



{"message":"Login credential expired, please log in again","status":40401}

  LEN: 360  

BODY: 'HTTP/1.1 401 Unauthorized\nDate: Sat, 10 Oct 2026 07:12:22 GMT\nContent-Type: application/json; charset=utf-8\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nServer: APISIX\n\n{"message":"Login credential expired, please log in again","status":40401}\n'



===== A1 AK 无参数 =====

STATUS: HTTP/1.1 200 

Date: Sat, 10 Oct 2026 07:12:25 GMT

Content-Type: application/json

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

X-Trace-Id: <TRACE>

X-API-Version: 20260917-41c7b8c

Server: APISIX



{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}  LEN: 1099  

BODY: 'HTTP/1.1 200 \nDate: Sat, 10 Oct 2026 07:12:25 GMT\nContent-Type: application/json\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nX-Trace-Id: <TRACE> 20260917-41c7b8c\nServer: APISIX\n\n{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}'



===== A2 AK tenantId=<UUID> =====

STATUS: HTTP/1.1 200 

Date: Sat, 10 Oct 2026 07:12:29 GMT

Content-Type: application/json

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

X-Trace-Id: <TRACE>

X-API-Version: 20260917-41c7b8c

Server: APISIX



{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}  LEN: 1099  

BODY: 'HTTP/1.1 200 \nDate: Sat, 10 Oct 2026 07:12:29 GMT\nContent-Type: application/json\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nX-Trace-Id: <TRACE> 20260917-41c7b8c\nServer: APISIX\n\n{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}'



===== A3 AK tenantId=<UUID> =====

STATUS: HTTP/1.1 200 

Date: Sat, 10 Oct 2026 07:12:32 GMT

Content-Type: application/json

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

X-Trace-Id: <TRACE>

X-API-Version: 20260917-41c7b8c

Server: APISIX



{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}  LEN: 1099  

BODY: 'HTTP/1.1 200 \nDate: Sat, 10 Oct 2026 07:12:32 GMT\nContent-Type: application/json\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nX-Trace-Id: <TRACE> 20260917-41c7b8c\nServer: APISIX\n\n{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}'



===== A4 AK tenantId=<UUID> =====

STATUS: HTTP/1.1 200 

Date: Sat, 10 Oct 2026 07:12:35 GMT

Content-Type: application/json

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

X-Trace-Id: <TRACE>

X-API-Version: 20260917-41c7b8c

Server: APISIX



{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}  LEN: 1099  

BODY: 'HTTP/1.1 200 \nDate: Sat, 10 Oct 2026 07:12:35 GMT\nContent-Type: application/json\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nX-Trace-Id: <TRACE> 20260917-41c7b8c\nServer: APISIX\n\n{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}'



===== A5 AK tenantId=<UUID> =====

STATUS: HTTP/1.1 200 

Date: Sat, 10 Oct 2026 07:12:39 GMT

Content-Type: application/json

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

X-Trace-Id: <TRACE>

X-API-Version: 20260917-41c7b8c

Server: APISIX



{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}  LEN: 1099  

BODY: 'HTTP/1.1 200 \nDate: Sat, 10 Oct 2026 07:12:39 GMT\nContent-Type: application/json\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nX-Trace-Id: <TRACE> 20260917-41c7b8c\nServer: APISIX\n\n{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}'



===== A6 AK 头 X-Tenant-Id=他人zetyun =====

STATUS: HTTP/1.1 200 

Date: Sat, 10 Oct 2026 07:12:42 GMT

Content-Type: application/json

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

X-Trace-Id: <TRACE>

X-API-Version: 20260917-41c7b8c

Server: APISIX



{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}  LEN: 1099  

BODY: 'HTTP/1.1 200 \nDate: Sat, 10 Oct 2026 07:12:42 GMT\nContent-Type: application/json\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nX-Trace-Id: <TRACE> 20260917-41c7b8c\nServer: APISIX\n\n{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}'



===== A7 AK body tenantId=<UUID> =====

STATUS: HTTP/1.1 200 

Date: Sat, 10 Oct 2026 07:12:45 GMT

Content-Type: application/json

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

X-Trace-Id: <TRACE>

X-API-Version: 20260917-41c7b8c

Server: APISIX



{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}  LEN: 1099  

BODY: 'HTTP/1.1 200 \nDate: Sat, 10 Oct 2026 07:12:45 GMT\nContent-Type: application/json\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nX-Trace-Id: <TRACE> 20260917-41c7b8c\nServer: APISIX\n\n{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}'



===== A8 AK aidcId=5 本租户基线 =====

STATUS: HTTP/1.1 200 

Date: Sat, 10 Oct 2026 07:12:49 GMT

Content-Type: application/json

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

X-Trace-Id: <TRACE>

X-API-Version: 20260917-41c7b8c

Server: APISIX



{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}  LEN: 1099  

BODY: 'HTTP/1.1 200 \nDate: Sat, 10 Oct 2026 07:12:49 GMT\nContent-Type: application/json\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nX-Trace-Id: <TRACE> 20260917-41c7b8c\nServer: APISIX\n\n{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}'



===== A9 AK aidcId=5+tenantId=<UUID> =====

STATUS: HTTP/1.1 200 

Date: Sat, 10 Oct 2026 07:12:52 GMT

Content-Type: application/json

Transfer-Encoding: chunked

Connection: keep-alive

Set-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800

X-Trace-Id: <TRACE>

X-API-Version: 20260917-41c7b8c

Server: APISIX



{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}  LEN: 1099  

BODY: 'HTTP/1.1 200 \nDate: Sat, 10 Oct 2026 07:12:52 GMT\nContent-Type: application/json\nTransfer-Encoding: chunked\nConnection: keep-alive\nSet-Cookie: <COOKIE>;path=/;HttpOnly;Max-Age=1800\nX-Trace-Id: <TRACE> 20260917-41c7b8c\nServer: APISIX\n\n{"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}'



DONE-1

<Objs Version="1.1.0.1" xmlns="http://schemas.microsoft.com/powershell/2004/04"><Obj S="progress" RefId="0"><TN RefId="0"><T>System.Management.Automation.PSCustomObject</T><T>System.Object</T></TN><MS><I64 N="SourceId">1</I64><PR N="Record"><AV>Preparing modules for first use.</AV><AI>0</AI><Nil /><PI>-1</PI><PC>-1</PC><T>Completed</T><SR>-1</SR><SD> </SD></PR></MS></Obj><Obj S="progress" RefId="1"><TNRef RefId="0" /><MS><I64 N="SourceId">1</I64><PR N="Record"><AV>Preparing modules for first use.</AV><AI>0</AI><Nil /><PI>-1</PI><PC>-1</PC><T>Completed</T><SR>-1</SR><SD> </SD></PR></MS></Obj></Objs>
```

### 2.3 这份原文为什么这么长

| 原因 | 体量 | 说明 |
|---|---:|---|
| 同一响应体被打印两遍（`BODY: repr` 再来一次） | 47%（9,461 字符） | 脚本 `show()` 既打印正文又打印 repr |
| 9 个 200 用例返回同一份 JSON | 31%（6,224 字符） | 去重后只剩 1 份响应体 |
| 响应头 | 11%（2,303 字符） | 20 个块各一套，只留 content-type/server/x-api-version |
| 空行 / `#< CLIXML` / LEN 行 | 1%（不到 200 字符） | 几乎不占，删了也省不到钱 |

### 2.4 改造后的完整替代文本（1,969 字符）

```text
A0 匿名基线 -> 401 | content-type:application/json; charset=utf-8 server:APISIX
  {"message":"Login credential expired, please log in again","status":40401}
A1 AK 无参数 -> 200 | content-type:application/json server:APISIX x-api-version:20260917-41c7b8c
  {"status":200,"message":"OK","data":[{"code":"ALab","name":"Alaya Lab","level":2,"items":[{"code":"alab_count","name":"Alaya Lab","unit":"个","isShare":0,"defaultLimit":20.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"CCI","name":"云容器实例","level":2,"items":[{"code":"cci_count","name":"云容器实例","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"HPT","name":"极核训练","level":2,"items":[{"code":"hpt_count","name":"极核训练","unit":"个","isShare":0,"defaultLimit":50.0000}],"parent_code":"COMPUTE","parent_name":"计算"},{"code":"NAS-STORAGE","name":"NAS型存储","level":2,"items":[{"code":"nas_storage_count","name":"NAS型存储","unit":"个","isShare":0,"defaultLimit":30.0000}],"parent_code":"STORAGE","parent_name":"存储"}]}
A2 AK tenantId=<UUID> -> 200 | content-type:application/json server:APISIX x-api-version:20260917-41c7b8c | 响应体同上 x2
A3 AK tenantId=<UUID> -> 200 | content-type:application/json server:APISIX x-api-version:20260917-41c7b8c | 响应体同上 x3
A4 AK tenantId=<UUID> -> 200 | content-type:application/json server:APISIX x-api-version:20260917-41c7b8c | 响应体同上 x4
A5 AK tenantId=<UUID> -> 200 | content-type:application/json server:APISIX x-api-version:20260917-41c7b8c | 响应体同上 x5
A6 AK 头 X-Tenant-Id=他人zetyun -> 200 | content-type:application/json server:APISIX x-api-version:20260917-41c7b8c | 响应体同上 x6
A7 AK body tenantId=<UUID> -> 200 | content-type:application/json server:APISIX x-api-version:20260917-41c7b8c | 响应体同上 x7
A8 AK aidcId=5 本租户基线 -> 200 | content-type:application/json server:APISIX x-api-version:20260917-41c7b8c | 响应体同上 x8
A9 AK aidcId=5+tenantId=<UUID> -> 200 | content-type:application/json server:APISIX x-api-version:20260917-41c7b8c | 响应体同上 x9
```

### 2.5 逐项对照

| 处理 | 内容 | 理由 |
|---|---|---|
| **原样保留** | 用例名、状态码、Content-Type、Server、X-API-Version | 判断依据（401=未授权；响应体看租户隔离是否生效） |
| **原样保留（首份）** | 每种不同的响应体完整 JSON | 证据正文，一字不截 |
| **折叠** | 与首份完全相同的响应体 → 响应体同上 xN | 第 18 次重复不产生第 18 条信息 |
| **丢弃** | Date / Connection / Transfer-Encoding / 空行 | 对判断无意义 |
| **掩码** | Set-Cookie、X-Trace-Id | 会话凭据，不该反复进上下文 |
| **落盘** | 原始 stdout 全文写文件，摘要末尾给路径 | 模型要细节时 Read，等于没删 |

落盘引用长这样：

```text
全文: data/tasks/81/i5713/cmd-output/out-<id>.txt（22,013 字节，可用 Read 读取）
```

---

## 3. 同一节点另外 12 轮的实测

| 轮 | 命令（截断） | 原始 | 改造后 | 省 |
|---:|---|---:|---:|---:|
| 1 | `cd E:\AI\series\data\tasks\81\i5713; python .\q1_items.py 2>&1` | 22,013 | 1,965 | 91% |
| 2 | `cd E:\AI\series\data\tasks\81\i5713; python .\q2_templates.py 2>` | 18,101 | 4,189 | 77% |
| 3 | `cd E:\AI\series\data\tasks\81\i5713; python .\q3_scope.py 2>&1 |` | 619 | 608 | 2% |
| 4 | `cd E:\AI\series\data\tasks\81\i5713; python .\q3_templates_scope` | 28,367 | 7,549 | 73% |
| 5 | `cd E:\AI\series\data\tasks\81\i5713; Get-Content cmd-output\..\.` | 1,462 | 1,449 | 1% |
| 6 | `cd E:\AI\series\data\tasks\81\i5713\cmd-output; python -c " impo` | 5,867 | 2,503 | 57% |
| 7 | `cd E:\AI\series\data\tasks\81\i5713\cmd-output; python -c " s=op` | 2,408 | 17 | 99% |
| 8 | `cd E:\AI\series\data\tasks\81\i5713; python .\q4_enum.py 2>&1` | 13,060 | 2,199 | 83% |
| 9 | `cd E:\AI\series\data\tasks\81\i5713; python .\q5_overview.py 2>&` | 10,225 | 10,214 | 0% |
| 10 | `cd E:\AI\series\data\tasks\81\i5713; python .\q6_final.py 2>&1` | 2,367 | 2,356 | 0% |
| 11 | `cd E:\AI\series\data\tasks\81\i5713; python -c " import hmac,has` | 6,230 | 2,240 | 64% |
| 12 | `cd E:\AI\series\data\tasks\81\i5713; python -c " import hmac,has` | 2,123 | 2,112 | 1% |
| 13 | `cd E:\AI\series\data\tasks\81\i5713; python .\q7_userid.py 2>&1` | 4,661 | 4,650 | 0% |
| **合计** | 13 次调用 | **117,503** | **42,051** | **64%** |

> 纯文本输出（日志、文件清单）识别不到 HTTP 块的，一律原样放行，不参与压缩。

---

## 4. 全库量级

| 指标 | 数值 |
|---|---:|
| 工具返回总量 | 3.5 亿字符（Bash 1.62 亿 / Grep 0.26 亿 / Read 0.26 亿） |
| 含 #< CLIXML 的返回 | 39,411 条（26.5%），1.09 亿字符，其中 29% 是纯 XML |
| 含 HTML 的返回（抽样 3,000 条） | 标记与脚本占 63% |
| 预期节省（**实测后修正**） | 2~3% 总成本（约 40~50 元 / 1,597 元） |

---

## 5. 验证与回滚

1. 只改工具结果的呈现层（tool_result 文本），不动工具执行、不动提示词、不动对话结构；
2. 原文全部落盘，摘要末尾带路径与字节数；
3. 同一任务跑两遍对比：tool_result 平均字符数（2,363 → ≤1,200）、每轮未缓存 token、发现数 / 复现率 / 报告完整度；
4. 任一质量指标下降即回滚——渲染函数独立一层，单 commit 可撤。

---

## 6. 敏感信息

本文档域名、密钥、租户 ID、Cookie、Trace-Id 已掩码；完整原文仍在本机 data/tasks/81/i5713/ 与数据库内，未外发。
