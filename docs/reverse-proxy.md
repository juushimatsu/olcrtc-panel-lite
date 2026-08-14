# Reverse proxy и скрытый путь панели

Панель может слушать только loopback, а внешний Nginx или Caddy обслуживать домен, безопасный сайт в `/`, панель по отдельному пути и подписки по другому пути.

Пример `/etc/olcrtc-panel/config.yaml`:

```yaml
listen: "127.0.0.1:8443"
public_ip: "203.0.113.10"
public_port: 443
public_origin: "https://example.com"
panel_path: "/control-a8f3"
subscription_path: "/feeds-b19c"
trusted_proxies:
  - "127.0.0.1/32"
  - "::1/128"
```

`public_origin` содержит только HTTPS origin без пути. `panel_path` и `subscription_path` задаются без завершающего `/`, не совпадают и не вложены друг в друга. После изменения этих полей перезапустите `olcrtc-panel.service`.

В `trusted_proxies` должен быть адрес непосредственного reverse proxy. Для proxy на другом хосте добавьте его точный CIDR, например `10.20.0.10/32`; не доверяйте всей внешней сети. Только доверенный peer может передавать клиентский адрес через `X-Forwarded-For`.

## Nginx

`proxy_pass` указан без URI, поэтому исходные `/control-a8f3/...` и `/feeds-b19c/...` передаются панели без удаления prefix. Заголовки Upgrade покрывают noVNC WebSocket внутри panel path.

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl http2;
    server_name example.com;

    ssl_certificate     /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;

    root /var/www/example;
    index index.html;

    location = /control-a8f3 {
        proxy_pass https://127.0.0.1:8443;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_ssl_verify off;
    }

    location ^~ /control-a8f3/ {
        proxy_pass https://127.0.0.1:8443;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_read_timeout 3600s;
        proxy_ssl_verify off;
    }

    location ^~ /feeds-b19c/ {
        proxy_pass https://127.0.0.1:8443;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_ssl_verify off;
    }

    location / {
        try_files $uri $uri/ =404;
    }
}
```

`proxy_ssl_verify off` допустим здесь только потому, что upstream доступен исключительно через loopback. Вместо этого можно скопировать private CA панели в доступный Nginx файл и включить `proxy_ssl_trusted_certificate` вместе с `proxy_ssl_verify on`.

## Caddy

Caddy автоматически проксирует WebSocket Upgrade. В примере forwarded headers заданы явно, а остальные запросы обслуживаются как отдельный статический сайт.

```caddyfile
example.com {
    @panel path /control-a8f3 /control-a8f3/*
    handle @panel {
        reverse_proxy https://127.0.0.1:8443 {
            transport http {
                tls_insecure_skip_verify
            }
            header_up Host {host}
            header_up X-Forwarded-Proto {scheme}
            header_up X-Forwarded-For {remote_host}
        }
    }

    @subscriptions path /feeds-b19c/*
    handle @subscriptions {
        reverse_proxy https://127.0.0.1:8443 {
            transport http {
                tls_insecure_skip_verify
            }
            header_up Host {host}
            header_up X-Forwarded-Proto {scheme}
            header_up X-Forwarded-For {remote_host}
        }
    }

    handle {
        root * /var/www/example
        file_server
    }
}
```

`tls_insecure_skip_verify` также должен использоваться только для loopback upstream. Для проверки private CA настройте Caddy transport с доверенным CA-файлом.

## Проверка

```bash
curl -I https://example.com/
curl -I https://example.com/control-a8f3
curl -I https://example.com/control-a8f3/
curl -I https://example.com/feeds-b19c/EXISTING_SLUG
```

Ожидается: `/` обслуживается отдельным сайтом, `/control-a8f3` перенаправляется на `/control-a8f3/`, API доступен только под panel path, а подписка доступна независимо под subscription path. noVNC проверяется запуском Playwright-сессии из панели и успешным подключением браузера к WebSocket.
