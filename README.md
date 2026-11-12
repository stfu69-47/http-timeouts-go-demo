# HTTP timeouts в Go: где запрос проводит время

Автор: [Артём Трофимов](https://github.com/stfu69-47).

Диагностическая лаборатория на стандартной библиотеке Go: ожидание соединения, DNS, TCP, TLS, заголовков и тела ответа. Измеряет HTTP/1.1-запрос до полного чтения и закрытия тела, сохраняет события `httptrace` и ошибки. Сетевая часть сопоставляет запросы с пакетами и TCP-счётчиками.

В репозитории находятся исходники, тесты, сценарии Docker/netem, инструменты анализа и полный проверенный прогон от 12 ноября 2026 года: **4890 запросов и шесть PCAP**. Новые запуски сохраняются отдельно в `runs-new/` и не изменяют приложенные измерения.

- [Устройство стенда и десять сценариев](http-timeout-lab/README.md).
- [Сетевая лаборатория и ограничения](http-timeout-lab/network/README.md).
- [Результаты и ограничения сохранённого прогона](runs/20261112T123100Z/README.md).
- [JSONL основной серии](runs/20261112T123100Z/suite/), [CPU и сокеты](runs/20261112T123100Z/observations/), [PCAP и TCP-счётчики](runs/20261112T123100Z/network/run-20261112T123155Z-6n9IQE/).
- [Проверка полноты](runs/20261112T123100Z/analysis/validation.json) и [связь повторных передач с задержками](runs/20261112T123100Z/analysis/retransmission-evidence.json).

## Проверить опубликованные измерения

В каталоге `runs/20261112T123100Z` лежат 1690 записей основной серии на macOS, 200 записей отдельного Linux-наблюдения CPU и сокетов и 3000 сетевых запросов в Linux. 40 ошибок ожидаемы в сценариях таймаутов; остальные 4850 ответов успешно прочитаны. Выборки разных окружений не объединяются.

Из корня проекта сначала проверьте SHA-256 каждого приложенного файла:

```sh
(cd runs/20261112T123100Z && shasum -a 256 -c SHA256SUMS.txt)
```

Для независимого пересчёта нужны Python 3.11+ и `tshark` в PATH. Следующие команды заново читают все JSONL и шесть PCAP, проверяют HTTP-пары и TCP-счётчики, затем строят графики. Отдельная копия сохраняет опубликованные результаты без изменений.

```sh
python3 -m venv .venv
.venv/bin/python -m pip install -r tools/requirements-plots.txt
mkdir -p runs-new
RUN=$(mktemp -d "$PWD/runs-new/verify-XXXXXX")
cp -R runs/20261112T123100Z/. "$RUN/"
.venv/bin/python tools/analyze_run.py "$RUN"
.venv/bin/python tools/data_charts.py --data "$RUN/suite" --out "$RUN/assets"
.venv/bin/python tools/network_chart.py "$RUN"
```

Ожидаемый итог проверки — `validated: 4890`, `expected_errors: 40`, по 1000 HTTP-пар в каждом PCAP и четыре запроса с повторными передачами в серии `delay20ms-loss1pct`: № 61, 64, 634 и 886. Список контрольных сумм и происхождение файлов — в [SOURCE-MANIFEST.json](runs/20261112T123100Z/SOURCE-MANIFEST.json). Исходные JSONL и PCAP совпадают с файлами измерительного запуска побайтно; в нескольких служебных метаданных локальные домашние пути заменены обозначениями `<KIT_ROOT>`, `<PROJECT_ROOT>` или `<HOME>`.

Захваты содержат все HTTP-пары, но счётчик received by filter превышает captured на 24 в каждом файле. Полнота завершающего TCP-обмена не доказана; детали сохранены вместе с результатами.

## Быстрый запуск основной серии

Требуются Go и C-компилятор для детектора гонок. В `go.mod` указан Go 1.23.0; фактическая проверка выполнена на Go 1.26.5, macOS/arm64. Внешних Go-зависимостей нет.

```sh
cd http-timeout-lab
go test -buildvcs=false -count=1 ./...
go test -buildvcs=false -race -count=1 ./...
go vet ./...
go build -buildvcs=false -o timeoutlab .
./timeoutlab suite -out results-new
```

Основная серия выполняет 1690 запросов. Два сценария специально проверяют ошибки до заголовков и при чтении тела. Не считайте HTTP 200 самостоятельным признаком успешного завершения.

## Повторить все три группы

Команды выполняются **из корня этого проекта**. Для сетевой части нужны Docker Engine в Linux VM, современный Docker Compose, Bash 5 и GNU `timeout`. На Mac Apple Silicon измерения выполнялись в OrbStack. Сетевой драйвер должен поддерживать `NET_ADMIN`, `NET_RAW` и `netem`; параметры применяются только к изолированным контейнерам.

```bash
set -e
RUN="runs-new/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$RUN/bin" "$RUN/checks" "$RUN/network" "$RUN/assets"

(cd http-timeout-lab && go test -buildvcs=false -count=1 ./...)
(cd http-timeout-lab && go test -buildvcs=false -race -count=1 ./...)
(cd http-timeout-lab && go vet ./...)
(cd http-timeout-lab && go build -buildvcs=false -o "../$RUN/bin/timeoutlab" .)
"$RUN/bin/timeoutlab" suite -out "$RUN/suite" \
  > "$RUN/checks/suite.stdout" 2> "$RUN/checks/suite.stderr"

docker build --platform linux/arm64 -f http-timeout-lab/network/Dockerfile \
  -t timeoutlab-reproduction:20261112 http-timeout-lab
docker build --platform linux/arm64 -f tools/Dockerfile.observe \
  -t timeoutlab-observe:20261112 .
docker run --rm --network none -v "$PWD:/work" -w /work \
  timeoutlab-observe:20261112 python3 tools/observe_linux.py \
  --binary /usr/local/bin/timeoutlab --out "$RUN/observations" \
  > "$RUN/checks/observe.stdout" 2> "$RUN/checks/observe.stderr"

# Mac с Homebrew Bash 5 и GNU coreutils:
export PATH="/opt/homebrew/opt/coreutils/libexec/gnubin:$PATH"
(cd http-timeout-lab && /opt/homebrew/bin/bash network/run.sh "../$RUN/network")
# На Linux вместо /opt/homebrew/bin/bash используйте bash.
```

Для x86-64 замените обе платформы сборки на `linux/amd64`. Контейнерная сборка использует Go 1.27.4; изменение toolchain создаёт новое окружение, которое нужно указать в отчёте. Каждая сетевая серия состоит из 1000 запросов: baseline, delay 20ms, delay 20ms + loss 1%. Направление воздействия — egress сервера. Скрипт сохраняет PCAP с обеих сторон, снимки `ss`, `nstat`, статистику `tc` и статус очистки. Порты на хост не публикуются.

## Проверка и графики

Для анализа нужны Python 3.11+, `tshark` в PATH и зависимости из `tools/requirements-plots.txt`:

```sh
python3 -m venv .venv
.venv/bin/python -m pip install -r tools/requirements-plots.txt

# После выполнения всех трёх групп выше:
.venv/bin/python tools/analyze_run.py "$RUN"
.venv/bin/python tools/data_charts.py --data "$RUN/suite" --out "$RUN/assets"
.venv/bin/python tools/network_chart.py "$RUN"
```

Анализ рассчитан на полную стандартную конфигурацию 1690 + 200 + 3000. Он проверяет число и ID записей, ошибки, перцентили, завершение сетевого запуска и пары HTTP-запрос/ответ в обоих PCAP. Это проверка конкретного стенда: она предполагает, что HTTP-запросы и короткие ответы целиком помещаются в наблюдаемые TCP-сегменты; для общего TCP-трафика нужен анализ с реконструкцией потока.

`tools/make_figures.py --out "$RUN/assets"` строит условную схему этапов. Остальные графики используют только данные выбранного нового запуска. Не объединяйте разные окружения и прогоны по общему полю `scenario: probe`.

## Ограничения

Это диагностический стенд с намеренными паузами и закрытой моделью нагрузки, без отдельного прогрева и без серии повторов. Он не измеряет предельную пропускную способность. p99 — ближайший ранг измеренной выборки, не гарантия задержки следующего запроса.

Проверяйте число HTTP-пар в обоих PCAP, stderr процесса захвата, счётчики captured / received by filter / dropped by kernel, статус завершения и очистки. Нулевые потери захвата в ядре сами по себе не доказывают полноту файлов. Метки повторных передач сопоставляйте с адресами, портами, raw sequence и содержимым пакетов; они не доказывают точное место каждой потери.
