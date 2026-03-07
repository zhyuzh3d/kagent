> 安全提示：本文示例中的鉴权信息仅使用占位符；真实 Token/密钥请放入 `configx.json` 或本地环境变量，禁止写入仓库与文档。

## flash对话大模型
[官方文档](https://www.volcengine.com/docs/82379/1399008?lang=zh)

```shell
curl https://ark.cn-beijing.volces.com/api/v3/responses \
-H "Authorization: Bearer $ARK_API_KEY" \
-H 'Content-Type: application/json' \
-d '{
    "model": "doubao-seed-1-6-flash-250828",
    "input": [
        {
            "role": "user",
            "content": [
                {
                    "type": "input_image",
                    "image_url": "https://ark-project.tos-cn-beijing.volces.com/doc_image/ark_demo_img_1.png"
                },
                {
                    "type": "input_text",
                    "text": "你看见了什么？"
                }
            ]
        }
    ]
}'
```

## chat对话大模型
[官方文档](https://www.volcengine.com/docs/82379/1399008?lang=zh)

```shell
curl https://ark.cn-beijing.volces.com/api/v3/responses \
-H "Authorization: Bearer <YOUR_TOKEN>" \
-H 'Content-Type: application/json' \
-d '{
    "model": "doubao-seed-2-0-pro-260215",
    "input": [
        {
            "role": "user",
            "content": [
                {
                    "type": "input_image",
                    "image_url": "https://ark-project.tos-cn-beijing.volces.com/doc_image/ark_demo_img_1.png"
                },
                {
                    "type": "input_text",
                    "text": "你看见了什么？"
                }
            ]
        }
    ]
}'
```
### 联网插件
[官方文档](https://www.volcengine.com/docs/82379/1338552?lang=zh)

```python
import os
from volcenginesdkarkruntime import Ark

client = Ark(
    base_url="https://ark.cn-beijing.volces.com/api/v3",
    api_key=os.getenv("ARK_API_KEY")
)

response = client.responses.create(
    model="doubao-seed-2-0-pro-260215",  # 替换为您使用的模型版本
    input=[{"role": "user", "content": "今天有什么热点新闻？"}],
    tools=[{
        "type": "web_search",
        "max_keyword": 2,  # 限制单轮搜索最大关键词数量，可按需调整
        "limit": 10  # 限制单次搜索返回结果数量
    }]
)
print(response)
```

## 语音识别
[官方文档](https://www.volcengine.com/docs/6561/1354869?lang=zh)

## 语音合成
[官方文档](https://www.volcengine.com/docs/6561/1329505?lang=zh)
