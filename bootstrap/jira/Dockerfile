# Локальная Jira Software 8.13.19 на Atlassian Plugin SDK.
#
# Зачем контейнер, если SDK ставится и нативно: инстанс становится сбрасываемым
# (удалить том, а не искать каталог на диске), не сорит в ~/.m2 хоста
# и одинаково поднимается на любой машине.
#
# Зачем SDK, если есть образ atlassian/jira-software: тот собран только под amd64
# (на arm64 идёт через Rosetta) и требует лицензию с my.atlassian.com. SDK выдаёт
# девелоперскую лицензию сам — на Core, Software и Service Desk разом, — а JDK
# и Jira-артефакты платформонезависимы, так что контейнер получается родной arm64.
FROM eclipse-temurin:11-jdk

# curl — для проверки готовности снаружи и изнутри; больше ничего не нужно.
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl \
 && rm -rf /var/lib/apt/lists/*

# SDK в git не едет: это 162 МБ артефактов. Читатель распаковывает его в sdk/ сам —
# как, сказано в README.md рядом.
#
# Кладётся он целиком: скрипты atlas-* вычисляют свой ATLAS_HOME сами, поэтому
# переносятся куда угодно.
COPY sdk/atlassian-plugin-sdk-8.2.10 /opt/atlassian-plugin-sdk
ENV PATH=/opt/atlassian-plugin-sdk/bin:$PATH

# Бандл SDK молча опирается на регистронезависимую файловую систему: артефакт
# javax.transaction:jta:1.0.1B лежит в каталоге `1.0.1b`, а maven ищет `1.0.1B`.
# На macOS это один каталог, в Linux — два, и сборка падает «missing artifacts».
# Скачать его неоткуда: Sun запретила класть jta в Central, а зеркало Atlassian
# отдаёт 404. Единственное расхождение во всём бандле — проверено обходом.
RUN ln -s 1.0.1b /opt/atlassian-plugin-sdk/repository/javax/transaction/jta/1.0.1B

# Проект нужен ради одной строчки — <applications>. Флагом её передать нельзя:
# у параметра нет maven-свойства, а atlas-run-standalone работает без POM.
COPY project /polygon
WORKDIR /polygon

# Данные — в томах: target держит и распакованную Jira, и её JIRA_HOME с H2,
# ~/.m2 — скачанные артефакты (около гигабайта, качаются один раз).
VOLUME ["/polygon/target", "/root/.m2"]
EXPOSE 2990

# Грабля, ради которой здесь `tail -f /dev/null`: atlas-run читает stdin и на EOF
# гасит Jira. Контейнер без -i отдаёт ему закрытый stdin, и инстанс выключался бы
# сразу после старта.
CMD ["/bin/sh", "-c", "tail -f /dev/null | atlas-run"]
