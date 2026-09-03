# Engineered by Dhanush C N (github.com/dhanush-cn)
FROM node:20-alpine AS builder
WORKDIR /app

COPY package*.json ./
RUN npm ci || npm install

COPY . .
# Vite inlines env vars at build time, so the gateway URL is a build argument.
ARG VITE_API_BASE_URL=http://localhost:8080
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL
RUN npm run build

FROM nginx:stable-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
