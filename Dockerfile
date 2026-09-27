# --------- 多阶段构建：先前端，再后端（前端产物嵌入 binary）---------
FROM node:20-alpine AS frontend-build
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --prefer-offline
COPY frontend/ .
RUN npm run build

# --------- 后端构建 ---------
FROM golang:1.26-alpine AS backend-build
WORKDIR /app
# 拷贝 go mod 文件，先下载依赖（利用 Docker 层缓存）
COPY backend/go.mod backend/go.sum ./
RUN go mod download
# 拷贝源码
COPY backend/ .
# 把前端产物复制到后端能找到的位置
COPY --from=frontend-build /app/frontend/dist ./frontend/dist
RUN go build -o bin/gwserver ./cmd/server

# --------- 运行阶段 ---------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone && \
    apk del ca-certificates tzdata
WORKDIR /app
COPY --from=backend-build /app/bin/gwserver .
COPY --from=backend-build /app/frontend/dist ./frontend/dist

EXPOSE 5176

ENV PORT=5176
ENV DB_TYPE=sqlite
ENV DB_PATH=/data/gateway.db

VOLUME ["/data"]

ENTRYPOINT ["./gwserver"]
