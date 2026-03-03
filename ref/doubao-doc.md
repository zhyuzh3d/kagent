> 安全提示：本文示例中的鉴权信息仅使用占位符；真实 Token/密钥请放入 `configx.json` 或本地环境变量，禁止写入仓库与文档。

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

## 语音识别
[官方文档](https://www.volcengine.com/docs/6561/1354869?lang=zh)

## 语音合成
[官方文档](https://www.volcengine.com/docs/6561/1329505?lang=zh)
