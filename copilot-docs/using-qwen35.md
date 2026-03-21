# Use Qwen 3.5 models

Created the branch `main-qwen35` and did the following:

```shell
git clone https://github.com/ollama/ollama.git .
git checkout 8224cce583e6e7253e2fdeee8f07ab4c8da7bce5
curl -L https://github.com/ollama/ollama/pull/14134.diff | patch -p1
docker build -t ollama/ollama:main-qwen35 .
```
