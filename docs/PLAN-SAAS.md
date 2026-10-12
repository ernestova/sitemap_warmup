# Plan de recalibración: sitemap_warmup → CLI open-source en Go + SaaS

> Estado: borrador v1.2 (2026-10-12). Modelo **open-core**. El CLI en Go es
> open-source (Apache-2.0, ver [ADR 0001](adr/0001-apache-license-for-open-core.md)) y trabaja
> desde **tu punto de sondeo** (tu máquina, tu servidor o tu runner de CI, con tu IP y tu red);
> los puntos de sondeo regionales, el historial, las alertas y el warming programado son el
> SaaS de pago. Stripe y la Inc. de Delaware ya existen, así que la facturación y la parte
> legal no bloquean. Vocabulario: ver [GLOSSARY.md](../GLOSSARY.md) (*origin* = el servidor
> del sitio detrás del CDN; *vantage point* / punto de sondeo = desde dónde salen las peticiones).

## 1. Reposicionamiento (lo que cambia respecto al repo actual)

**Cliente principal**: desarrolladores y equipos de plataforma que despliegan sitios detrás
de un CDN; agencias como segundo segmento.

**Propuesta de valor doble, al mismo nivel**: **calentar** y **diagnosticar**.

*"Calienta tu caché desde N regiones después de cada deploy o purga, y verifica qué URLs de
tu sitemap no se cachean, por qué, y cuáles no son indexables."*

Ejemplo del mensaje: "Desde 12 regiones, el 40 % de tu sitemap es MISS en CloudFront
São Paulo: la plantilla `/producto/*` envía `Set-Cookie` y `Cache-Control: private`."

Matiz honesto que debe estar en el copy: la caché es por PoP y Tiered Cache / Akamai Prefetch
ya resuelven parte del problema; calentar desde un solo punto de sondeo solo afecta al PoP más
cercano, y Google no indexa más rápido por calentar. Por eso el warming útil es el regional
y post-deploy, y siempre se confirma con una segunda pasada (HIT).

## 2. Frontera open-source / SaaS

| Funcionalidad | OSS (CLI + Action) | SaaS |
|---|---|---|
| Parseo de sitemap (index, `.gz`, `lastmod`) | ✅ | ✅ |
| Estado HTTP, TTFB real, redirecciones | ✅ | ✅ |
| Detección de CDN + HIT/MISS/age | ✅ (desde tu punto de sondeo) | ✅ (puntos de sondeo regionales) |
| Diagnóstico de no-cacheabilidad por plantilla | ✅ | ✅ + tendencias |
| Indexabilidad (meta robots, `X-Robots-Tag`, canonical) | ✅ | ✅ |
| Salida JSON / tabla / SARIF, exit code para CI | ✅ | — |
| Diff contra la ejecución anterior (archivo local) | ✅ | ✅ (historial) |
| **Geolocalización: chequeo y warming desde N regiones** | ❌ | ✅ |
| Warming programado / disparado por webhook de deploy o purge | ❌ | ✅ |
| Historial, dashboards, alertas (email/Slack/webhook) | ❌ | ✅ |
| Vista multi-sitio para agencias, reportes white-label | ❌ | ✅ |
| Verificación de dominio, cuotas, rate limiting gestionado | n/a | ✅ obligatorio |

Regla: todo lo que corre **desde tu punto de sondeo** (tu máquina, tu red, tu CI; ves y calientas
solo el PoP del CDN más cercano a ti) es libre; todo lo que requiere
**infraestructura distribuida, estado o tiempo** es de pago.

## 3. Reevaluación tecnológica: Go

**Decisión: reescribir en Go (greenfield, no un port).** El `main.py` actual son 218 líneas
con fallos conocidos (no lee `cf-cache-status`/`x-cache`/`Age`; una sesión HTTP por URL;
mide solo el tiempo de conexión y no el TTFB; `DataFrame.append` eliminado en pandas 2;
`depth` como string; tests rotos). Reescribir cuesta poco, y ese es el argumento real,
más que "Go es rápido".

Por qué Go encaja:
- **Distribución**: binario estático único → GoReleaser → GitHub Releases, Homebrew tap,
  imagen Docker `scratch` y GitHub Action con el mismo binario. Nada de virtualenv ni pandas.
- **El mismo código en los workers regionales del SaaS** (el CLI es la librería del worker).
- **stdlib suficiente**: `net/http` (reutiliza conexiones, HTTP/2), `net/http/httptrace`
  (DNS / conexión / TLS / TTFB reales), `encoding/xml` + `compress/gzip` (sitemaps),
  `golang.org/x/net/html` (meta robots, canonical), `golang.org/x/time/rate` (rate limiting).
- Concurrencia con `errgroup` + semáforo; cancelación con `context`.

Stack propuesto:
- CLI: `cobra` + config YAML opcional (`sitewarm.yml`).
- CDN soportados a fondo **desde el MVP**: Cloudflare, Vercel, CloudFront, Fastly y Akamai.
- Tests: `httptest` simulando cabeceras de Cloudflare, Fastly, Akamai, CloudFront y Vercel;
  golden files para JSON/SARIF.
- Release: GoReleaser + firma con cosign; `golangci-lint` en CI (sustituye Travis/Sonar).

Alternativa descartada: Rust (más curva y menos velocidad de iteración para un equipo de una
persona) y seguir en Python (distribución peor para un CLI/Action y para workers).

**Repositorios: dos repos separados.**

1. **`ernestova/sitemap_warmup` → open-source (público, Apache-2.0).** Se conserva por sus
   años de historial, estrellas y credibilidad. La reescritura en Go vive aquí: se añade
   `go.mod`, el Python se mueve a `legacy/` y se etiqueta `v0-python`. Se puede renombrar más
   adelante (GitHub redirige las URLs y los remotes).
2. **Repo nuevo y privado para el SaaS** (p. ej. `ernestova/<producto>-cloud`). Contiene API,
   panel, scheduler, workers regionales, Stripe e infraestructura. Importa el motor OSS como
   dependencia Go (`go get github.com/ernestova/sitemap_warmup@vX.Y.Z`), con versiones fijas.
   El nombre del producto se decide en la Fase 0.

Consecuencias de diseño:
- En Go, `internal/` **no se puede importar desde otro módulo**: el motor reutilizable va en
  paquetes públicos `pkg/` con una API estable (semver). Solo el código propio del CLI queda
  en `internal/`.
- Nada del SaaS (multi-región, historial, facturación) entra en el repo OSS; así la frontera
  open-core queda clara y no hay que vigilar fugas.
- Los workers regionales del SaaS son un `main` del repo privado que llama a `pkg/probe` y
  `pkg/diagnose`: el mismo motor que usa la comunidad, probado por ella.
- Las mejoras al motor se hacen primero en el OSS (PR público → release) y después se sube la
  versión en el SaaS.

Estructura del repo OSS:
```
cmd/sitewarm/          # CLI
pkg/sitemap/           # fetch + parse (index, gz)              ← importable por el SaaS
pkg/probe/             # cliente HTTP, httptrace, cabeceras     ← importable
pkg/cdn/               # detección por proveedor                ← importable
pkg/diagnose/          # reglas de cacheabilidad/indexabilidad  ← importable
pkg/report/            # tabla, JSON, SARIF, diff
internal/cli/          # flags, config, wiring del CLI
action.yml             # GitHub Action
legacy/                # Python original (v0-python)
```

Estructura del repo SaaS (privado):
```
cmd/api/               # API + panel
cmd/worker/            # worker regional (usa pkg/ del OSS)
cmd/scheduler/         # cola river: scans periódicos y por webhook
internal/billing/      # Stripe Checkout, Portal, webhooks, cuotas
internal/domains/      # verificación DNS TXT / meta / archivo
internal/store/        # Postgres (migraciones, consultas)
deploy/fly/            # fly.toml por región
```

## 4. Requisitos innegociables (OSS y SaaS)

- **User-Agent honesto y verificable** (`sitewarm/1.x (+https://<dominio>/bot)`); nunca
  suplantar Googlebot (se verifica por DNS inverso y los WAF lo bloquean).
- Respetar `robots.txt` por defecto; concurrencia y RPS limitados por defecto.
- SaaS: **verificación de dominio** (registro DNS TXT o meta tag / archivo) antes de **cualquier
  escaneo o warming**, programado o manual ([ADR 0003](adr/0003-verify-domain-before-any-hosted-scan.md)).
  Única excepción: el **chequeo público** gratuito, una sola vez, ≤ 50 URLs, sin verificar,
  como gancho de captación. Tope de requests por plan; botón de pausa; IPs de salida publicadas.
- Cabeceras de diagnóstico opcionales que el cliente puede permitir en su WAF.

## 5. Fases con puertas de validación

### Fase 0 — Decisiones (1 semana)
- Nombre, dominio, licencia (Apache-2.0 se mantiene), frontera OSS/SaaS (tabla de arriba).
- Landing con lista de espera y una sola frase de propuesta de valor.

### Fase 1 — CLI en Go MVP (2–3 semanas)
- `sitewarm scan <sitemap-url>`: estado, TTFB, CDN, HIT/MISS, `age`, `cache-control`,
  `vary`, `set-cookie`, meta robots, `X-Robots-Tag`, canonical.
- Agrupación por plantilla de URL (normalizar segmentos numéricos/slug → `/producto/*`).
- `--format table|json|sarif`, `--fail-on noindex,5xx,uncacheable`.
- `sitewarm warm --changed-since <fecha|archivo>`: calienta solo las URLs con `lastmod` nuevo
  (dos pasadas: la segunda confirma HIT).
- Tests con `httptest` y cobertura > 70 %.

### Fase 2 — GitHub Action + lanzamiento OSS (1–2 semanas)
- `uses: ernestova/sitewarm-action@v1` después del deploy; comenta en el PR o falla el build.
- Publicar en GitHub Marketplace, Homebrew, Show HN, r/webdev, comunidades de Astro, Next.js
  y WordPress, y comunidades en español.

### 📊 Métricas de validación (el SaaS se construye en paralelo)
Decisión: el SaaS **no espera** a la validación; se construye en paralelo al CLI. Estas
métricas se siguen igual, a ~6 semanas del lanzamiento, como señal de demanda
(**al menos dos**):
- ≥ 300 estrellas o ≥ 50 repos usando la Action.
- ≥ 100 en la lista de espera con dominio corporativo.
- ≥ 10 entrevistas, de las cuales ≥ 3 dicen que pagarían por multi-región/alertas.
- ≥ 3 prepagos (Stripe Payment Link) a precio de fundador.

Si no se cumplen: **se congela el gasto del SaaS** (se apagan los puntos de sondeo regionales,
el código queda en el repo privado) y se sigue solo con el open-source hasta que haya señal.

### Fase 3 — SaaS MVP (4–6 semanas)
- **API + panel**: Go (`chi` o `net/http`) + Postgres (Neon/Supabase) + front ligero
  (htmx o Next.js). Auth: magic link o GitHub OAuth.
- **Scheduler**: cola en Postgres (`river`) para scans periódicos y por webhook.
- **Workers regionales**: el mismo binario en **Fly.io** con máquinas fijadas por región
  (Cloudflare Workers no permite elegir la región de salida). Arranque: 6 regiones
  (iad, sjc, gru, fra, sin, syd) + qro/mex si hay tracción en LatAm.
- **Funciones**: verificación de dominio, matriz región × plantilla (HIT %), historial,
  alertas (email, Slack, webhook), warming post-deploy (webhooks de Vercel, Netlify,
  GitHub y purgas de Cloudflare).
- **Stripe**: Checkout + Customer Portal + webhooks (`checkout.session.completed`,
  `customer.subscription.updated/deleted`, `invoice.payment_failed`); cuotas por plan
  aplicadas en la API; Stripe Tax activado (las ventas SaaS generan nexo de sales tax en
  varios estados de EE. UU. aunque la Inc. sea de Delaware; IVA para clientes de la UE/LatAm).
- **Legal mínimo**: términos, privacidad, AUP (prohíbe usarlo contra dominios no verificados), DPA.

### Fase 4 — Crecimiento (continuo)
- Vista de agencia (multi-sitio, white-label, PDF).
- Integraciones: API de Cloudflare (leer las reglas de caché y explicar el MISS), Search Console
  (cruzar indexabilidad con cobertura real).
- Contenido SEO técnico en español e inglés ("por qué mi CDN no cachea X").

## 6. Precios (anclados a la competencia)

Referencia: warmup.rocks $9 / $23.40 / $59.40 al mes; PageSpeedPlus y Cache Igniter en rangos similares.

Unidad de cobro: **por sitio**, con tope de URLs por plan.

| Plan | Precio | Incluye |
|---|---|---|
| OSS | $0 | CLI + Action desde tu punto de sondeo, ilimitado en tu propia infraestructura |
| Starter | $12/mes | 1 sitio, 3 regiones, 5k URLs, scan diario, alertas por email |
| Pro | $39/mes | 5 sitios, 8 regiones, 50k URLs, scan horario + post-deploy, Slack/webhook |
| Agency | $99/mes | 25 sitios, todas las regiones, white-label, API |

Anual con 2 meses gratis. Precio de fundador −40 % de por vida para los primeros 50.

**Coste unitario estimado**: escanear 50k URLs × 8 regiones es ~400k requests; en Fly
(shared-cpu, máquinas que se apagan al terminar) cuesta pocos centavos por scan, y el
egress es mínimo porque se pueden usar peticiones con `Range` o descartar el cuerpo tras
leer el `<head>`. Margen objetivo > 85 %.

## 7. Riesgos y mitigaciones

| Riesgo | Mitigación |
|---|---|
| Fácil de copiar | Velocidad, comunidad OSS, nicho de diagnóstico por plantilla, español |
| Abuso (DDoS a terceros) | Verificación de dominio, cuotas, UA honesto, IPs publicadas, AUP |
| Bloqueo por WAF del cliente | Documentar la allowlist de IPs y UA; modo "solo cabeceras" (HEAD) |
| CDN que añaden funciones nativas | Ser multi-CDN y agnóstico; el valor está en el diagnóstico |
| Poca demanda | Puerta de validación antes de construir el SaaS |

## 8. Próximos pasos inmediatos

1. Elegir nombre y dominio; crear la landing con lista de espera (Stripe Payment Link de fundador).
2. En el repo OSS, crear la rama `go-rewrite`: `go.mod`, mover el Python a `legacy/`, esqueleto de `cmd/` y `pkg/`.
   Crear el repo privado del SaaS solo después de pasar la puerta de validación (puede existir
   vacío antes para reservar el nombre).
3. Implementar `internal/sitemap` + `internal/probe` con tests `httptest`.
4. Publicar `v0.1.0` del CLI + Action y empezar a medir contra la puerta de validación.
