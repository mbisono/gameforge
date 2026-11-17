# GameForge

[English](README.md)

Aplicativo com interface gráfica (Go + Tk, via [modernc.org/tk9.0](https://gitlab.com/cznic/tk9.0)) para Windows e Linux que junta o **jogo base**, a **atualização** e as **DLCs** de um jogo de Nintendo Switch (NSP, NSZ ou XCI) em um único arquivo, por padrão **comprimido em NSZ** para ocupar menos espaço, para fazer backup dos seus jogos comprados.

Em outras abas, o app também cuida de imagens de disco de outros consoles: **Discos** (PS1, PS2, Saturn, Sega CD, Dreamcast, PSP…: conversão para CHD, informações do jogo e nomes do Redump), **Xbox → XISO** (Xbox e Xbox 360), **GameCube/Wii → RVZ** e **ISO → CSO/ZSO** (PSP e PS2), e **ROMs** comprime ROMs de cartucho ou recomprime arquivos ZIP em 7z, renomeia ROMs com os nomes do No-Intro e as verifica no RetroAchievements.

Versão atual: **2.11.0** (as versões 1.x eram o nspmerge, só para o Switch). O botão **Sobre…**, ao lado de idioma e tema, mostra a versão, o que o app faz e as bibliotecas usadas.

**Versões novas:** na primeira vez que abre, o app pergunta se pode verificar uma vez por dia se há uma versão nova. Se você aceitar, ele consulta no GitHub a release mais recente quando abre (no máximo uma vez por dia) e, se houver uma mais nova, mostra um link para ela no topo da janela e uma linha no log. Só o nome e a versão do app são enviados (como `GameForge/2.11.0`), nada sobre você ou os seus arquivos, e nada é baixado ou instalado sozinho; sem conexão, ou com o GitHub bloqueado, nada acontece. A verificação pode ser ligada ou desligada em **Sobre…** (`update_check` no `gameforge.json`).

## Download

Executáveis prontos para Windows e Linux (64 bits) estão na página de [Releases](https://github.com/tschertel/gameforge/releases), com o código-fonte de cada versão. O que mudou em cada versão está no [CHANGELOG.md](CHANGELOG.md) (em inglês). Para compilar a partir do código, veja [Compilação](#compilação).

## Abas

A janela tem uma aba por plataforma/formato: **Nintendo Switch**, **Xbox → XISO**, **GameCube/Wii → RVZ**, **ISO → CSO/ZSO**, **Discos** e **ROMs**. Idioma, tema, **Verificar após gravar**, o log, a barra de progresso e **Cancelar** valem para todas. A pasta de **Saída** é de cada aba: abrir uma pasta numa aba (ou adicionar ou arrastar arquivos) faz dessa pasta a saída da aba, então trocar de função não exige escolher de novo; dá para mudar à mão, e com o campo vazio cada resultado é gravado ao lado do arquivo. Cada aba tem sua própria lista, com **Abrir pasta…**, **Adicionar arquivos…**, **Limpar lista** e o botão que executa a conversão; nada é convertido só por entrar na lista. A aba mostrada por último volta na próxima execução.

Arquivos arrastados para a janela (ou pastas, lidas recursivamente) vão para a aba aberta, se ela aceita o formato; senão, para a aba padrão do formato: `.nsp`/`.nsz`/`.xci` para Nintendo Switch, `.cue` e `.gdi` para Discos e `.iso` para Xbox → XISO quando é uma imagem de Xbox (o app procura o sistema de arquivos XDVDFS), para GameCube/Wii → RVZ quando é de GameCube ou Wii (e também `.gcm`), ou para Discos nos outros casos. Para comprimir uma `.iso` em CSO/ZSO, abra a aba ISO → CSO/ZSO antes de arrastar (ou use os botões dela); o mesmo vale para ROMs, que aceita arquivos de qualquer extensão. Se a aba atual não recebeu nada, o app mostra a aba que recebeu. Arquivos soltos sobre o próprio executável (ou abertos com "Abrir com") entram do mesmo jeito.

Nas abas de conversão, a lista tem as colunas **Plataforma** (detectada pelo conteúdo do disco), **Detalhes** e **Status**: ao clicar em converter, cada linha mostra "Na fila", "Convertendo 43%", "Verificando 80%" e, no fim, o tamanho do resultado e quanto diminuiu, ou o motivo de um erro ("⏭ já existe", "✗ …", "■ cancelado").

Novos formatos entram no código como um "conversor" (extensões aceitas, descrição de cada arquivo, nome de saída e a conversão em si, em `tabs.go`), que ganha uma aba igual às outras.

## Como funciona

Um NSP é um contêiner PFS0 com arquivos NCA, um `.cnmt.nca` (metadados), tickets (`.tik`) e certificados (`.cert`). A mesclagem copia todos esses arquivos, **sem alterá-los**, para um novo NSP com vários títulos (base + update + DLC). Arquivos com o mesmo nome (NCAs idênticas, certificados repetidos) entram só uma vez.

O `prod.keys` é usado apenas para **ler** os metadados: o app decifra o cabeçalho e o CNMT de cada NSP para saber o Title ID, o tipo (base/atualização/DLC) e a versão exatos. Sem o `prod.keys`, esses dados são deduzidos do nome do arquivo (`Nome [0100XXXXXXXXXXXX][v65536].nsp`). O mesmo acontece com jogos lançados para um firmware mais novo que o das suas chaves: a barra de chaves mostra até qual firmware o seu `prod.keys` vai, e esses arquivos aparecem com "⚠ precisa das chaves do firmware …". Nesse caso, extraia as chaves de novo de um console atualizado.

### Compressão NSZ (padrão)

Com a opção **Comprimir para NSZ** (ligada por padrão, requer o `prod.keys`), o resultado é um `.nsz` no formato do [nsz](https://github.com/nicoboss/nsz), aceito por instaladores como DBI e Tinfoil:

- As NCAs de dados do jogo (tipo *Program* e *PublicData*: o grosso do jogo, da atualização e das DLCs) são decifradas e comprimidas com zstd em blocos de 1 MiB, virando `.ncz`. As chaves para recifrar ficam no próprio arquivo; o instalador reconstrói a NCA original **byte a byte**, com as assinaturas da Nintendo intactas.
- NCAs pequenas (metadados, ícone/controle, manual), tickets e certificados são copiados sem alteração.
- Atualizações têm a criptografia por subseções (BKTR) tratada separadamente, para que também comprimam bem.
- Fragmentos delta das atualizações (NCAs *Data*, usados só para atualizações incrementais pelo console) são deixados de fora, como o nsz faz.
- A economia depende do jogo: jogos com dados já comprimidos economizam pouco, outros chegam a 30–50%.

Níveis: **Rápido**, **Normal** (padrão) e **Máximo** (bem mais lento, ganho pequeno).

**Verificar após gravar** (ligado por padrão) relê o arquivo gerado, descomprime cada NCZ e compara o SHA-256 com o da NCA original; se algo não bater, o arquivo é apagado e o erro aparece no log. Durante a gravação, o hash de cada NCA também é comparado com o nome do arquivo (que é o próprio hash): se não bater, o log avisa que o NSP de origem pode estar corrompido.

Compatibilidade conferida com o `nsz` 5.0.0 oficial: o `nsz -D` restaura o NSZ gerado por este app para um NSP idêntico byte a byte ao merge sem compressão; o app descomprime corretamente NSZs gerados pelo `nsz`; e a tabela de seções das NCZ é idêntica nas duas ferramentas.

Sem compressão, o app gera um `.nsp` comum, apenas reempacotando os arquivos como antes.

### Descomprimir NSZ → NSP

Arquivos `.nsz` também entram na lista. Com **Comprimir para NSZ** desligado, as NCZ deles são descomprimidas e o resultado é um `.nsp` comum, com as NCAs originais **byte a byte** (o nome de cada NCA é o hash dela, e **Verificar após gravar** confere). Para só converter um NSZ, adicione o arquivo, desmarque a compressão e clique em **Mesclar selecionados**. Fragmentos delta que o NSZ descartou continuam de fora (não fazem falta para jogar). O modo **Aplicar atualização no jogo base** ainda não lê NSZ: descomprima antes.

### XCI (imagem de cartucho)

Arquivos `.xci` também entram na lista. Um XCI guarda as NCAs do jogo na partição *secure* do cartucho (num formato parecido com o PFS0, o HFS0); o app usa essas NCAs como se fossem um NSP, então um XCI pode ser convertido em NSP/NSZ e mesclado com atualizações e DLCs. NCAs de cartucho não usam ticket. Funcionam XCIs com ou sem a área de chaves de 0x1000 bytes no início, inclusive os "trimmed". XCZ (XCI comprimido) não é suportado.

### Atualizar um arquivo já mesclado

Como a mesclagem não altera nada, o jogo base original continua dentro do arquivo mesclado. Quando sair uma atualização nova, dá para instalá-la por cima no console ou no emulador, ou mesclar de novo o arquivo mesclado com a atualização nova: a lista mostra o que o arquivo já inclui (por exemplo, `inclui atualização 1.0.8 (v524288)`) e, na mesclagem, a atualização antiga que estava dentro dele é trocada pela nova (base e DLCs continuam).

Arquivos com a atualização *integrada* ao jogo base (versão maior que 0 num jogo base, como os `[patched]` gerados pela opção abaixo) aparecem como "atualização integrada, não aceita outra": uma atualização só se aplica ao jogo base original (v0), então o app não os combina com atualizações.

### Aplicar a atualização no jogo base (só emuladores)

O ícone **ⓘ** ao lado da opção mostra um resumo desta seção quando o mouse para sobre ele.

Com **Aplicar atualização no jogo base**, em vez de guardar base e atualização como títulos separados, o app gera **um único jogo base já atualizado** (`… [patched].nsp`/`.nsz`). Ocupa menos espaço que o merge normal, porque as partes do jogo base que a atualização substituiu são descartadas, mas **não aceita mais nenhuma atualização**: vale a pena para jogos que não vão mais ser atualizados.

- A seção RomFS da atualização (BKTR) é uma tabela que diz, para cada trecho do RomFS novo, se ele vem do jogo base ou dos dados da atualização. O app lê essa tabela e grava o RomFS completo resultante.
- O ExeFS (código), os dados de controle (ícone/NACP) e os manuais vêm da atualização; o que só existe no jogo base (ex.: o logo) é mantido. Fragmentos delta e atualizações mais antigas são ignorados. DLCs marcadas entram sem alteração.
- Um NSP já mesclado (base + atualização) também serve de entrada: o app usa a base e a atualização que estão dentro dele.
- As NCAs novas não usam mais ticket (a chave vai para a área de chaves) e ganham um CNMT novo, de *Application* na versão da atualização.
- Com **Verificar após gravar**, o app relê o RomFS gerado e confere toda a árvore de hashes IVFC: qualquer trecho montado errado aparece como erro no log.
- Com **Comprimir para NSZ** ligado, o resultado é comprimido em seguida.

**Limites desta opção:** as assinaturas RSA da Nintendo deixam de valer. Emuladores não as verificam; um console só aceitaria com patches de assinatura. Não é reversível: guarde os arquivos originais. Jogos que usam armazenamento esparso ou comprimido (alguns títulos recentes) ou tabelas de patch muito grandes ainda não são suportados: o log avisa.

Por padrão vêm marcadas só a atualização mais recente (as atualizações são cumulativas) e a versão mais nova de cada DLC, mas você escolhe arquivo por arquivo.

### Discos: CHD, informações do jogo e nomes

Na aba **Discos**, arquivos `.cue`, `.gdi` e `.iso` entram na lista já marcados, com a plataforma, o tipo e o tamanho de cada um. Nada é convertido ao adicionar: marque os que quiser e clique em **Converter para CHD**. O resultado é o formato **CHD v5** do MAME, aceito por DuckStation, PCSX2, Flycast, PPSSPP, RetroArch e MAME. Não precisa do `prod.keys`. A conversão é feita pelo próprio app, em Go, sem o `chdman`:

- **`.cue` + `.bin`** → CHD de CD (PS1, Saturn, Sega CD, PC Engine CD…), no mesmo layout do `chdman createcd`: blocos de 8 setores de 2352 bytes + 96 de subcódigo, faixas completadas até múltiplos de 4 setores, faixas de áudio gravadas em big-endian e uma entrada de metadados `CHT2` por faixa (com pregap `INDEX 00` ou `PREGAP`). Aceita um `.bin` por faixa ou um `.bin` único. Setores Mode 1 e Mode 2 Form 1 (os dados de PS1) com ECC íntegro têm o sync e a paridade removidos e regenerados na leitura, como no `chdman` atual (em Mode 2 o cabeçalho conta como zero no cálculo do ECC, como manda a norma).
- **Dreamcast (GD-ROM)** → CHD de GD-ROM (metadados `CHGD`), como o `chdman createcd`: tanto o `.gdi` clássico quanto o `.cue` multi-faixa do redump (o que tem `REM SINGLE-DENSITY AREA` e `REM HIGH-DENSITY AREA`). A área de alta densidade começa no setor 45000, e o espaço até ela é preenchido com setores vazios. O layout segue o `chdman` 0.289, a versão lançada: no `.gdi`, a distância até a faixa seguinte vira setores vazios no fim de cada faixa; no `.cue` do redump, o pregap de cada faixa passa para o fim da faixa anterior (o layout do `.gdi`). O código-fonte mais novo do MAME (ainda não lançado) troca parte desse preenchimento por pregaps; os emuladores leem os dois.
- **`.iso`** → CHD de **CD** ou de **DVD**, conforme o jogo: imagens de PS1 e imagens de até 737.280.000 bytes (um CD de 80 minutos, como os jogos de PS2 em CD) viram CHD de CD (uma faixa Mode 1, como o `chdman createcd` faz com uma `.iso`); PSP e imagens maiores viram CHD de DVD, como o `chdman createdvd`. A coluna Detalhes mostra a escolha ("→ CHD de CD (automático)"); se ela estiver errada para algum jogo, clique na linha com o botão direito e escolha **Converter como CD** ou **Converter como DVD**.
- **Informações do jogo:** a coluna Detalhes mostra o que o disco diz sobre o jogo, antes dos detalhes da imagem: por exemplo `SCES-00001 · Europa` para Ridge Racer (PS1), `SLUS-21409 · v1.01 · EUA` (PS2) ou `AEROWINGS · T40201N · v1.002 · EUA` (Dreamcast). Discos de PSP trazem título, código, versão, região, número do disco e firmware mínimo (do `PARAM.SFO`); Dreamcast, Saturn e Sega CD trazem título, código, versão, regiões e número do disco (do cabeçalho); PS1 e PS2 trazem só o código do jogo (do `SYSTEM.CNF`), a região que ele indica e, no PS2, a versão: o nome do jogo não está nesses discos. Imagens de GameCube e Wii mostram o ID e o título. Jogos de PSP também mostram o ícone (`ICON0.PNG`) ao lado do nome, na proporção dele, e jogos de Dreamcast em CHD ou GDI mostram o rótulo do disco (`0GDTEX.PVR`, a imagem impressa no disco), quando o disco tem um: muitos jogos mais antigos não têm. Os nomes ficam alinhados mesmo quando só algumas linhas têm imagem. **CHDs que você já tem** também entram na lista, lidos direto do CHD (feitos pelo app ou pelo `chdman`, de CD, GD-ROM ou DVD), com o tipo de disco e o número de faixas; eles não têm caixa de marcar, porque não há o que converter, e o botão direito oferece **Verificar CHD…**.
- **Renomear…** dá aos CHDs e ISOs da lista os nomes do [Redump](http://redump.org), os nomes mais usados em backups de disco: `Ridge Racer.chd` → `Ridge Racer (Europe).chd`, `legostarwarsiitheoriginaltrilogy.chd` → `LEGO Star Wars II - The Original Trilogy (USA).chd`. O jogo é achado pelo código no disco (PS1, PS2, PSP, Dreamcast, Saturn, Sega CD, GameCube e Wii). Quando várias entradas têm o mesmo código (outras revisões, regiões ou edições), decidem as regiões e, em GameCube e Wii, a revisão que o disco informa; depois o tamanho da faixa de dados e, só se precisar, o CRC dela, que lê a faixa inteira (uns 20 segundos para uma faixa de 1,1 GB de Dreamcast). Se nem assim der para decidir, ou o código não estiver na base, o arquivo fica como está e o log diz o motivo. Os nomes vêm das bases do Redump publicadas pelo libretro ([libretro-database](https://github.com/libretro/libretro-database), CC BY-SA 4.0): na primeira vez que discos de um console são renomeados, o app pede para baixar a base dele (alguns MB por console; uns 12 MB para os oito) e guarda o que usa em `cache/redump.cache`, ao lado do `gameforge.json`. O menu **Base de nomes ▾** atualiza as bases de todos os consoles (só os arquivos que mudaram são baixados de novo), e o texto ao lado mostra quais consoles estão nela e até que data ela tem dados, com uma sugestão de atualizar depois de 30 dias. Sem a base, discos de PSP, GameCube e Wii, que trazem o título, recebem esse título com a região. Como nos arquivos do Switch, as mudanças aparecem antes, nenhum arquivo é substituído e cada mudança fica no log; playlists `.m3u` da mesma pasta que citam um arquivo renomeado são atualizadas, e os arquivos do RetroArch com o nome do jogo na mesma pasta vão junto (`Ridge Racer.state.auto`, `Ridge Racer.png`, `Ridge Racer.srm`… viram `Ridge Racer (Europe).state.auto`…), então saves, states e capturas continuam valendo; a prévia mostra quantos são. Imagens `.cue` e `.gdi` não são renomeadas (os arquivos das faixas também teriam de mudar): converta antes.
- **Verificar RetroAchievements** mostra, na coluna de status, se cada CHD, ISO, CUE ou GDI é uma versão que o RetroAchievements reconhece, em discos de PS1, PS2, PSP, Dreamcast, Saturn e Sega CD, como o botão da aba ROMs faz com cartuchos (mesma chave de API, listas e cache). O console é o que o disco informa, e o hash segue o rcheevos: o executável de boot indicado no `SYSTEM.CNF` com o nome dele (PS1, PS2), o `PARAM.SFO` e o `EBOOT.BIN` (PSP), o início do `IP.BIN` e o arquivo de boot (Dreamcast, achado na última faixa de um GD-ROM), ou os primeiros 512 bytes do disco (Saturn, Sega CD). Só esses arquivos são lidos, então um disco leva uma fração de segundo, mesmo em CHD. Discos de Dreamcast num `.cue` do Redump não são verificados: converta-os antes para CHD.
- **Plataforma:** o app reconhece PS1 e PS2 (pelo `SYSTEM.CNF`), PSP (`UMD_DATA.BIN`/`PSP_GAME`), Saturn, Sega CD e Dreamcast (pelo cabeçalho da Sega no primeiro setor), 3DO, Neo Geo CD (`IPL.TXT`) e PC Engine CD; em CDs só de áudio mostra "Audio CD". Imagens de GameCube e Wii também são reconhecidas.
- **Playlist `.m3u`:** com **Criar playlist .m3u para jogos com vários discos** (ligado por padrão), ao converter discos com nomes como `Jogo (Disc 1)`, `Jogo (Disc 2)` (também `Disk`, `CD2` ou `Disc 2 of 3`), o app grava `Jogo.m3u` na pasta de saída com a lista dos CHDs desse jogo que estão lá, em ordem. Abrindo o `.m3u` no RetroArch ou no DuckStation dá para trocar de disco durante o jogo. Uma playlist que já está em dia não é regravada.
- Cada bloco é comprimido com todos os codecs e fica o menor. Em CD: `cdlz` (LZMA), `cdzl` (deflate) e `cdfl` (FLAC, para faixas de áudio), como o `chdman`; em DVD: `lzma` e `zlib`. Todos os leitores de CHD suportam esses codecs. O FLAC é implementado no app: preditores fixos (ordem 0 a 4) e predição linear (LPC, até ordem 12, com coeficientes calculados por autocorrelação e Levinson-Durbin sobre janelas de Tukey), escolha entre estéreo independente, esquerda/lado, lado/direita e meio/lado pelo tamanho real de cada opção, e códigos de Rice por partição. Blocos repetidos (ex.: áreas vazias) são gravados uma vez só.
- **Verificar após gravar** relê o CHD, descomprime todos os blocos e confere o CRC de cada um e o SHA-1 gravado no cabeçalho; se algo não bater, o arquivo é apagado. O botão **Verificar CHD…** da aba confere um `.chd` que você já tem, inclusive os gerados pelo `chdman` (o app lê os codecs `cdlz`, `cdzl`, `cdfl`, `lzma`, `zlib`, `huff` e `flac`; não lê `cdzs`/`zstd` nem CHDs com arquivo pai).

Compatibilidade conferida com o `chdman` 0.289 oficial: o `chdman verify` aceita os CHDs gerados (inclusive os blocos FLAC); o SHA-1 dos dados **e** o SHA-1 geral (que inclui os metadados das faixas) são idênticos aos de um CHD criado pelo `chdman createcd`/`createdvd` com a mesma entrada (inclusive uma `.iso` convertida como CD, um `.gdi` e um `.cue` de Dreamcast); e o `chdman extractcd` devolve o mesmo CUE/BIN dos dois. No outro sentido, o app lê e verifica os CHDs criados pelo `chdman`.

**Limites:**

- O tamanho fica praticamente igual ao do `chdman`, com SHA-1 idêntico. Em jogos de PS1 reais só de dados: Colony Wars (Disc 1), 347,3 MB contra 346,9 MB (+0,1%), e X-Men vs. Street Fighter, 227,26 MB contra 227,53 MB (−0,1%). Em jogos com faixas de áudio de CD: Ridge Racer, 310,38 MB contra 309,91 MB (+0,15%), e Ridge Racer Revolution, 530,28 MB contra 529,55 MB (+0,14%); sem a predição linear (LPC) do FLAC, eram 4,9% e 4,3% maiores. A pequena diferença que sobra vem do codificador LZMA em Go, que comprime um pouco menos que o do 7-Zip, e de detalhes do FLAC da libFLAC. Em DVD a diferença é de uns 2%, porque o app também não grava os codecs `huff` e `flac` que o `chdman` tenta.
- `.cue` com faixas em WAV e a conversão CHD → CUE/BIN não são suportadas.

### Converter imagens de Xbox para XISO

Na aba **Xbox → XISO**, imagens `.iso` de Xbox e Xbox 360 (por exemplo, as do redump) viram `Nome.xiso.iso`, o formato que o xemu, o Xenia e consoles desbloqueados carregam. Não precisa do `prod.keys`.

- A imagem de um disco tem uma partição de vídeo e depois a partição do jogo, num sistema de arquivos XDVDFS; os setores que nenhum arquivo usa são preenchidos com dados aleatórios. O app acha a partição do jogo (Xbox: XGD1; Xbox 360: XGD2 e XGD3) e grava de novo só as tabelas de diretório e os arquivos, um depois do outro, como o `extract-xiso -r`. Uma imagem que já é XISO também é aceita e regravada sem espaço sobrando.
- O resultado costuma ser bem menor que a imagem original (que tem uns 7,3 GB no Xbox e mais no Xbox 360).
- As tabelas de diretório originais são mantidas (só a posição de cada arquivo muda), e o conteúdo dos arquivos é copiado sem alteração: ao contrário do `extract-xiso`, o app **não** aplica o patch de "media check" nos `.xbe`.
- Com **Verificar após gravar**, o app relê a XISO gerada e confere o SHA-1 de cada arquivo com o do original; se algo não bater, o arquivo é apagado.
- A lista mostra a plataforma, o tipo de imagem (imagem do disco XGD1/XGD2/XGD3 ou XISO), o número de arquivos e o total de dados do jogo. Jogos de Xbox mostram a imagem do título (a da lista de jogos do console, do `default.xbe`) ao lado do nome; jogos de Xbox 360 não mostram imagem.

### Comprimir GameCube e Wii em RVZ

Na aba **GameCube/Wii → RVZ**, imagens de disco de GameCube e Wii (`.iso`, `.gcm`) viram **RVZ**, o formato comprimido do Dolphin. Não precisa do `prod.keys`.

- Os discos de GameCube têm muito preenchimento pseudoaleatório (às vezes mais da metade do disco), que nenhum compressor consegue reduzir. O RVZ descobre a semente do gerador que fez esse preenchimento e guarda só ela (68 bytes no lugar de cada trecho), e comprime o resto com Zstandard no nível mais alto da biblioteca. Para achar o preenchimento logo depois de cada arquivo, o app lê a tabela de arquivos (FST) do disco. O gerador e a recuperação da semente são uma tradução do código do Dolphin que está em domínio público (CC0); o resto segue a especificação do formato (`docs/WiaAndRvz.md` do Dolphin).
- A lista mostra a plataforma, o ID e o título do jogo.
- **Wii:** as partições do disco são criptografadas e têm árvores de hash (H0/H1/H2) a cada 32 KiB. O RVZ guarda os dados **descriptografados e sem as hashes**, o que deixa o preenchimento aleatório reconhecível e o resto comprimível; quem lê o RVZ (o Dolphin) recalcula as hashes e criptografa de novo com a chave de cada partição, que fica guardada no próprio RVZ. Onde a hash original difere da recalculada (discos com hashes incomuns), o RVZ guarda a original ("exceções"), então a ISO volta idêntica byte a byte. A tabela de partições, os tickets e o resto do disco fora das partições ficam como dados brutos.
- **Chave comum do Wii:** para converter ISO de Wii → RVZ, o app precisa descriptografar a chave de cada partição, e para isso usa a **chave comum do Wii**, que o app **não contém**: coloque a sua na pasta `keys` como `wii_common_key.bin` (16 bytes) ou `wii_common_key.txt` (32 dígitos hexadecimais), ou escolha o arquivo em **Chave comum do Wii: Procurar…** abaixo da lista. Discos coreanos usam a chave coreana (`wii_korean_key.bin`, ao lado). Sem a chave, os discos de Wii aparecem em cinza; se a chave não descriptografar o disco, o app avisa antes de gravar. GameCube não precisa de chave.
- **Tamanho do bloco** (abaixo da lista): 128 KiB (padrão, como o Dolphin sugere), 512 KiB, 1 MiB ou 2 MiB. Blocos maiores geram arquivos menores, mas o emulador descomprime o bloco inteiro a cada leitura de um trecho novo. 128 KiB é o mais seguro para handhelds e processadores fracos; 1 ou 2 MiB economizam espaço para jogar no PC. O nível de compressão não pesa ao jogar: o Zstandard descomprime praticamente na mesma velocidade em qualquer nível.
- Com **Verificar após gravar**, o app descomprime o RVZ gerado e confere o SHA-1 com o da imagem original.
- Compatibilidade conferida com o `DolphinTool` do Dolphin 2609b, em GameCube e em Wii: o Dolphin converte o RVZ do app de volta para uma ISO idêntica à original (no Wii, recalculando hashes e criptografia por conta própria), e o app lê os RVZ criados pelo Dolphin (conferido também com Super Smash Bros. Melee e Mario Kart Wii reais, em blocos de 2 MiB).
- Jogos de GameCube mostram o banner (a imagem de 96 × 32 do menu do console, do `opening.bnr` do disco) ao lado do nome, em ISOs, GCMs e RVZs; num RVZ, só os pedaços com a tabela de arquivos e o banner são descompactados. Discos de Wii ainda não mostram imagem.
- **Renomear…** dá aos ISOs, GCMs e RVZs da lista os nomes do Redump, como na aba Discos: `melee.rvz` → `Super Smash Bros. Melee (USA) (En,Ja) (Rev 2).rvz`. Os RVZs aparecem na aba para isso (com o ID do jogo, o título e o tamanho da imagem, lidos do cabeçalho do RVZ sem descompactar), mas não são convertidos. O jogo é achado pelo ID, e entre entradas pela região, pela revisão do disco e pelo tamanho da imagem; só se isso não decidir o CRC é calculado, o que num RVZ descompacta o disco inteiro. As bases de GameCube e Wii são as mesmas da aba Discos, baixadas uma vez quando precisar; os arquivos do RetroArch e as playlists `.m3u` acompanham os arquivos renomeados.
- Tamanho em jogos reais, comparado com o Dolphin (Zstandard nível 5, blocos de 128 KiB): com blocos de 128 KiB o app fica praticamente igual (+0,2% a +0,9%); com 1 MiB, de 1,5% a 5,8% menor; com 2 MiB, de 2,1% a 7,1% menor (Pikmin, Melee, Wind Waker, Mario Kart Wii e Super Mario Galaxy).

### Comprimir ISO em CSO/ZSO

Na aba **ISO → CSO/ZSO**, imagens `.iso` (PSP, PS2 e outras com setores de 2048 bytes) são comprimidas em **CSO** ou **ZSO**, formatos que loaders e emuladores leem direto, sem descomprimir antes. Não precisa do `prod.keys`.

- **CSO** usa deflate e é o mais aceito (PPSSPP, PCSX2, Open PS2 Loader, PSP com firmware customizado). **ZSO** usa LZ4: comprime um pouco menos, mas descomprime bem mais rápido, o que ajuda no console de verdade; confira se o seu loader ou emulador lê ZSO. A escolha fica em **ISO para:** abaixo da lista e vale para as próximas conversões.
- O arquivo é gravado na versão 1 do formato, com blocos de 2048 bytes; cada bloco é comprimido separado (deflate no nível máximo ou LZ4 HC) e, se não diminuir, é guardado sem compressão. Imagens acima de 2 GiB usam o deslocamento de índice previsto no formato.
- Com **Verificar após gravar**, o app descomprime o arquivo gerado e confere o SHA-1 com o da imagem original; se não bater, o arquivo é apagado.

### ROMs: comprimir em 7z, renomear, verificar no RetroAchievements

Na aba **ROMs**, cada arquivo vira o seu próprio `.7z`, com o mesmo nome: ROMs de cartucho (`Jogo (USA).gbc` → `Jogo (USA).7z`) ou arquivos ZIP, como sets de arcade, que viram um 7z com os mesmos arquivos dentro, com os mesmos nomes e datas. Não precisa do `prod.keys`.

- **Quais arquivos:** abaixo da lista, a aba mostra as extensões dos arquivos abertos, com quantos arquivos têm cada uma, como caixas de marcar (`.zip (142)`, `.auto (87)`, `.png (87)`…). Só as extensões marcadas entram na lista e são comprimidas. Os arquivos do RetroArch começam desmarcados (saves e states como `.auto`, `.srm` e `.state*`, capturas como `.png`, e `.cfg`, `.lpl`, `.cue`, `.m3u`, patches e textos); qualquer outra extensão começa marcada, então uma pasta de `.gbc` com os seus `.auto` e `.png` normalmente não precisa de ajuste. As escolhas ficam lembradas. Save states nunca são comprimidos.
- **Compressão:** LZMA, com o codificador do LZMA SDK (o mesmo do 7-Zip) portado para Go, no nível normal do 7-Zip (5), num fluxo sólido por arquivo. Níveis mais altos só aumentam o dicionário além do tamanho de uma ROM: num arquivo de 18 MB, o nível máximo do 7-Zip ficou só 0,13% menor que o normal. O resultado fica praticamente do tamanho do 7-Zip (11,48 MB contra 11,44 MB nesse arquivo; um ZIP no nível máximo tinha 12,35 MB), mas o codificador em Go é umas 1,5 a 3,5 vezes mais lento que o 7-Zip em uma thread, por volta de 1 a 2 MB por segundo: alguns segundos para uma ROM de cartucho ou um set de arcade comum, mas uns 30 segundos para uma ROM de GBA de 32 MB.
- **ZIP:** são lidos arquivos comprimidos com deflate ou guardados sem compressão; ZIPs com senha e outros métodos são recusados, com o motivo. O CRC de cada arquivo é conferido com o do ZIP (o MAME e o FBNeo identificam as ROMs por ele).
- **Apagar os originais depois de verificar:** remove cada original depois que o 7z dele é gravado, relido e comparado com ele, o que sempre acontece com esta opção, mesmo com **Verificar após gravar** desligado. Se a verificação falhar, o original fica. No Windows os originais vão para a Lixeira (em unidades sem Lixeira, como alguns pendrives e unidades de rede, o Windows os apaga); no Linux são apagados.
- **Arquivos ao mesmo tempo:** quantos arquivos são comprimidos de uma vez (1 por padrão). O compressor de 7z usa um núcleo do processador por arquivo, então vários ao mesmo tempo terminam antes, mas não na mesma proporção, porque os núcleos dividem a memória e a energia do processador: num processador de notebook com 12 threads (2 núcleos de desempenho e 8 de eficiência), quatro jogos de Neo Geo de 90 MB levaram 2 minutos juntos, contra uns 70 segundos cada um em sequência (4 minutos e 40 segundos). A lista vai até 8 (nunca mais que as threads do processador) e marca como recomendado a metade das threads, até 4. Cada arquivo sendo comprimido ocupa uns 100 MB de memória. Num disco lento (cartão SD, pendrive, pasta de rede), o disco pode virar o limite, e mais arquivos ao mesmo tempo ganham pouco; a quantidade gravada é a mesma em qualquer caso. As linhas do log dos arquivos se misturam, cada linha de erro diz de qual arquivo é, e a barra de progresso mostra o lote inteiro.
- **RetroArch:** save states e capturas têm o nome do jogo sem a extensão, então continuam valendo com o `.7z`. As playlists (`.lpl`) guardam o nome do arquivo com a extensão antiga: escaneie a pasta de novo no RetroArch. O FBNeo e o MAME leem 7z direto; para os outros núcleos, o RetroArch extrai o jogo numa pasta temporária antes.
- Com **Verificar após gravar**, o app relê o arquivo, descomprime tudo e compara o tamanho e o CRC de cada arquivo com o que foi gravado.
- Compatibilidade conferida com o 7-Zip 26: o `7z t` aceita os arquivos e o `7z x` extrai os arquivos originais, inclusive pastas dentro de um ZIP, arquivos vazios e nomes com acentos; o leitor do app também abre arquivos do 7-Zip com cabeçalho sem compressão.
- ROMs de Nintendo DS mostram o ícone (a imagem de 32 × 32 do menu do DS, do banner da ROM) ao lado do nome, soltas ou em ZIP ou 7z. Num 7z só o começo da ROM é descompactado, em segundo plano, então o ícone aparece um pouco depois da lista.
- **Renomear…** dá às ROMs, ZIPs e 7z da lista os nomes do [No-Intro](https://no-intro.org), os nomes mais usados em backups de cartucho: `pokemon crystal.gbc` → `Pokemon - Crystal Version (USA, Europe) (Rev 1).gbc`, `pkmn.zip` → `Pokemon - Crystal Version (USA, Europe) (Rev 1).zip`. Cartuchos não trazem um código do jogo confiável, então a ROM é achada pelo CRC-32 e pelo tamanho: em ZIPs e 7z eles vêm do cabeçalho do arquivo compactado, sem descompactar (os 7z aparecem na aba para isso, mas não são comprimidos de novo; o app lê também os arquivos do próprio 7-Zip); ROMs soltas são lidas uma vez. Uma ROM com um cabeçalho que não faz parte do cartucho, ou em outra ordem de bytes, também é achada: cabeçalhos de NES (iNES), Famicom Disk System (fwNES), Lynx e Atari 7800, cabeçalhos de copiadora de SNES, e arquivos `.v64` e `.n64` de Nintendo 64. Só o arquivo é renomeado, com a mesma extensão; a ROM dentro de um arquivo compactado fica com o nome dela, e nada é recomprimido. Textos e imagens dentro do arquivo compactado (como o `.txt` de um site de download) não contam; arquivos compactados com mais de uma ROM, como sets de arcade (`sf2.zip`, `ffight.zip`), nunca são renomeados, porque o MAME e o FBNeo acham o jogo por esse nome curto; ROMs que não estão na base (hacks, traduções, homebrew, dumps ruins) também ficam com o nome, e o log diz o motivo de cada uma. Os nomes vêm das bases do No-Intro publicadas pelo libretro ([libretro-database](https://github.com/libretro/libretro-database)), para 29 consoles (Nintendo do Game Boy ao DS, Sega do SG-1000 ao 32X, PC Engine, WonderSwan, Neo Geo Pocket, Atari, ColecoVision, Intellivision, Vectrex), além da lista do próprio libretro de jogos de Neo Geo no formato de arquivo único `.neo` (NeoSD, MiSTer, Geolith), cujos nomes não importam para os emuladores: `sonicwi2.zip` com `sonicwi2.neo` → `Aero Fighters 2 - Sonic Wings 2.zip` (os títulos ocidental e japonês ficam unidos por " - "; o `.neo` dentro fica com o nome curto). Na primeira vez que ROMs de um console são renomeadas, o app pergunta se pode baixar a base dele e guarda o que usa em `cache/nointro.cache`, ao lado do `gameforge.json`; o menu **Base de nomes ▾** atualiza todas (uns 12 MB). Como na aba Discos, as mudanças aparecem antes de renomear, nenhum arquivo é substituído, cada mudança vai para o log, e os arquivos do RetroArch com o nome do jogo vão junto (`pokemon crystal.srm` → `Pokemon - Crystal Version (USA, Europe) (Rev 1).srm`…).
- **Verificar RetroAchievements** mostra, na coluna de status, se cada ROM, ZIP ou 7z é uma versão que o [RetroAchievements](https://retroachievements.org) reconhece, para os achievements funcionarem no RetroArch: `✓ RA: Pokemon Crystal Version (53 achievements)`, `✓ RA: … (ainda sem achievements)` ou `✗ RA: versão não reconhecida`. Quando o RetroAchievements tem o jogo, mas não esta versão (por exemplo, só o lançamento europeu), o status diz `✗ RA: só outra versão é suportada` e o log traz o link da página com as versões suportadas; os nomes são comparados sem pontuação, sem "the" e sem os parênteses de região e idioma (`God of War - Chains of Olympus (USA)` corresponde a `God of War: Chains of Olympus`). Não olha o seu progresso. O app calcula o hash pelo qual o RetroAchievements identifica um jogo, com as regras dele para cada console (o arquivo inteiro na maioria dos consoles; NES, Famicom Disk System, Lynx, Atari 7800, SNES e PC Engine sem o cabeçalho extra; Nintendo 64 na ordem de bytes `.z64`; partes da ROM no DS, como faz o rcheevos), descompactando ZIPs e 7z para isso, e procura nas listas de jogos da API web do RetroAchievements. A API precisa da sua **chave de API**: na primeira vez, o app pede (depois de entrar no retroachievements.org, ela fica nas configurações da sua conta, em Keys); fica guardada só no `gameforge.json` e só é enviada ao RetroAchievements, e o menu **Base de nomes ▾** troca ou remove a chave. As listas dos consoles da aba são baixadas quando precisar e guardadas em `retroachievements.cache`; o menu as atualiza. Arquivos compactados com vários arquivos (sets de arcade) e ROMs cuja extensão não diz o console (como `.bin`) não são verificados. O log traz o hash de cada versão não reconhecida, para comparar com a página do jogo no RetroAchievements.

## Uso

Os passos abaixo são da aba **Nintendo Switch**; as outras abas funcionam do mesmo jeito, com os botões de conversão delas.


1. Coloque o `prod.keys` na pasta `keys/` (ao lado do executável). Ele é carregado automaticamente; também são procurados o último arquivo usado, `prod.keys` ao lado do executável e `~/.switch/prod.keys`. Ou clique em **Procurar…**.
2. Clique em **Abrir pasta…** (ou, no Windows, arraste uma pasta para a janela). Abrir uma pasta **substitui** a lista atual e limpa o log; a pasta é lida recursivamente. Na janela de escolha, basta selecionar a pasta e clicar em OK (não é preciso entrar nela). Use **Adicionar arquivos…** (ou arraste arquivos, no Windows) para incluir arquivos avulsos sem limpar a lista. São lidos arquivos `.nsp`, `.nsz` e `.xci` (outros formatos arrastados vão para as abas deles).
3. A lista mostra cada jogo com todos os seus arquivos (base, atualizações, DLCs). Com o `prod.keys`, o nome e o ícone do jogo, a versão legível (por exemplo, `1.0.8 (v524288)`) e o firmware mínimo exigido vêm do próprio NSP; o nome também é usado no arquivo de saída. Já vêm marcados a base, a atualização mais recente e a versão mais nova de cada DLC; clique na caixa de seleção (ou selecione a linha e aperte espaço) para marcar ou desmarcar. A caixa do jogo marca/desmarca todos os arquivos dele. **Marcar padrão** e **Desmarcar tudo** ajudam com listas grandes. Arquivos com exatamente o mesmo conteúdo de outro (por exemplo, o mesmo update baixado duas vezes, ou um NSP e o NSZ comprimido dele) aparecem em cinza como "cópia idêntica de …" e não vêm marcados.
4. Confira a pasta de saída (ela passa a ser a pasta aberta; mude se quiser, ou deixe vazia para gravar ao lado do primeiro arquivo de cada jogo) e clique em **Mesclar selecionados**. É gerado um arquivo (`.nsz` ou `.nsp`) por jogo com arquivos marcados.

Durante a gravação e a verificação, a linha de status mostra o progresso, a velocidade e o tempo que falta (estimado a partir da velocidade até ali, depois dos primeiros segundos).

O tamanho e a posição da janela (ou se ela estava maximizada), a largura das colunas da lista e a posição da divisória entre a lista e o log são lembrados de uma execução para a outra. Se a janela tinha ficado num monitor que não está mais conectado, ela volta centralizada.

O arquivo é gravado como `.part` e renomeado só quando termina, então cancelar nunca deixa um arquivo quebrado. A saída se chama `Nome [AppID][vVersão][N DLC].nsz` (ou `.nsp`).

O arquivo mesclado precisa de um instalador que aceite vários títulos num arquivo e, no caso do NSZ, o formato NSZ (por exemplo, DBI e Tinfoil). Emuladores variam no suporte a NSZ. Para voltar a um NSP comum, veja [Descomprimir NSZ → NSP](#descomprimir-nsz--nsp). **Guarde os originais** até confirmar que o arquivo mesclado instala e roda.

## Verificar um backup

**Verificar backup…** abre um `.nsp` ou `.nsz` que você já tem e confere se ele continua íntegro:

- cada NCA é lida inteira (as NCZ são descomprimidas) e o SHA-256 dela é comparado com o que os metadados (CNMT) registram; NCAs que nenhum CNMT lista são comparadas com o próprio nome, que é o hash. Como o hash cobre todos os bytes, isso também garante as árvores de hashes internas (IVFC);
- tudo o que os metadados listam precisa estar no arquivo (fragmentos delta podem faltar: não são necessários para jogar, e o NSZ os descarta);
- toda NCA que precisa de ticket precisa ter o ticket no arquivo.

O resultado aparece no log ("tudo íntegro" ou a lista de problemas). Sem o `prod.keys` só os nomes dos arquivos podem ser conferidos.

## Verificar online (titledb)

O botão **Atualizações e DLCs ▾** (aba Nintendo Switch) compara os jogos da lista com o [titledb](https://github.com/blawar/titledb), uma base pública de jogos do Switch:

- **Atualizar base de jogos** baixa do GitHub as versões de atualização de todos os jogos (`versions.json`) e o catálogo em inglês com os nomes de jogos e DLCs (`US.en.json`), cerca de 90 MB. O app guarda só o que usa em `cache/titledb.cache` (cerca de 1 MB), ao lado do `gameforge.json`. Todas as bases baixadas ficam na pasta `cache` (as versões anteriores as guardavam ao lado do `gameforge.json`; elas são movidas para lá ao abrir o app). Num novo download, arquivos que não mudaram no servidor não são baixados de novo.
- **Verificar os jogos da lista** mostra no log, para cada jogo, se existe uma atualização mais nova do que a que você tem (contando a de um arquivo mesclado ou de um jogo base com a atualização aplicada), quais DLCs faltam e quais DLCs têm versão mais nova. Se a base ainda não foi baixada, o app avisa e pergunta se quer baixá-la.
- Com a base carregada, a coluna de detalhes de cada jogo mostra um resumo: "atualizações e DLCs em dia (online)", "atualização mais nova disponível: v655360 de 2026-05-12 • 2 DLCs que não estão na pasta" ou "não consta na base online".

A internet só é usada quando você escolhe **Atualizar base de jogos**; a verificação é feita no computador, sem enviar nada sobre os seus jogos. Ao lado do botão, a aba mostra o estado da base: ainda não baixada, ou até que data ela tem dados, com uma sugestão de atualizá-la se tiver mais de 14 dias. Nada disso aparece no log ao abrir o app. A data importa porque o titledb é atualizado por voluntários, a cada poucas semanas, então atualizações muito recentes podem ainda não estar lá.

Os DLCs vêm do catálogo dos EUA: DLCs lançados só em outras regiões (por exemplo, só no Japão) não aparecem como faltando, e um jogo que não existe nesse catálogo aparece como "sem dados" se também não tiver atualizações listadas.

## Renomear arquivos do Switch

O botão **Renomear…** da aba Nintendo Switch dá aos arquivos da lista o nome padrão, útil quando o nome ficou errado no dump ou incompleto:

- Jogo base: `Nome do Jogo [0100XXXXXXXX0000][v0].nsp` (um NSP já juntado leva também a versão da atualização e `[n DLC]`, como os arquivos que o app gera).
- Atualização: `Nome do Jogo [0100XXXXXXXX0800][v196608].nsp`.
- DLC: `Nome do Jogo - Nome da DLC [0100XXXXXXXXX001][v0].nsp`.

O nome do jogo é o oficial, lido de dentro do próprio arquivo (precisa do `prod.keys`); sem ele, vem da base de jogos (**Atualizações e DLCs ▾ → Atualizar base de jogos**). Os nomes das DLCs vêm da base de jogos. `:` vira ` - `, símbolos como ™ saem e caracteres proibidos no Windows viram `_`. A extensão (`.nsp`, `.nsz`, `.xci`) não muda. Antes de renomear, o app mostra a lista "nome atual → nome novo" e pede confirmação; um arquivo nunca substitui outro com o mesmo nome, e cada mudança fica no log.

## Tema

O seletor **Tema**, ao lado do idioma, tem **Automático** (segue o modo claro/escuro do Windows; no Linux, claro), **Claro** e **Escuro**. A troca é imediata. No tema escuro, a barra de título também fica escura no Windows; as janelas de mensagem nativas do Windows continuam claras.

## Janelas de abrir arquivo e pasta

No Linux (e no Windows quando o idioma do app é diferente do sistema), o app usa as janelas de arquivo e pasta do próprio Tk. Elas escondem arquivos e pastas ocultos (nomes que começam com ponto, como `.cache` ou `.config`, e no Windows também os que têm o atributo de oculto) e têm a caixa **Mostrar arquivos e pastas ocultos** (na janela de pasta, **Mostrar pastas ocultas**); o estado dela fica lembrado (`show_hidden` na configuração). As janelas nativas do Windows seguem a configuração do Explorer.

## Idioma

A interface está em inglês, português, alemão, francês e espanhol. Por padrão ("Automático") ela segue o idioma do sistema; se ele não for um desses, usa inglês. Troque pelo seletor **Idioma** no canto superior direito; a janela é redesenhada na hora, sem perder a lista nem o log, e as mensagens que já estão no log também passam para o novo idioma. Os diálogos de abrir arquivo/pasta também seguem o idioma escolhido: no Linux eles são desenhados pelo próprio Tk, que tem tradução para os cinco idiomas; no Windows o app usa os diálogos nativos quando o idioma escolhido é o do sistema e, quando não é (os nativos sempre usam o idioma do Windows), os diálogos do Tk, traduzidos.

## Configuração

As opções ficam em `gameforge.json`, na pasta do programa (ao lado do executável; com `go run`, na pasta atual). O arquivo é criado na primeira execução (um `nspmerge.json` de quando o app se chamava NSP Merge é renomeado para `gameforge.json` e as opções continuam valendo) e atualizado quando você muda algo:

```json
{
  "language": "auto",
  "theme": "auto",
  "keys_path": "C:\\jogos\\gameforge\\keys\\prod.keys",
  "output_dir": "",
  "last_dir": "",
  "format": "nsz",
  "level": "normal",
  "skip_verify": false,
  "apply_update": false,
  "window": "1184x721+360+160",
  "columns": [460, 620],
  "divider": 330,
  "tab": "switch"
}
```

- `language`: `auto`, `en`, `pt`, `de`, `fr` ou `es`.
- `theme`: `auto`, `light` ou `dark`.
- `keys_path`: último `prod.keys` carregado (a pasta `keys/` tem prioridade).
- `output_dirs`: pasta de saída de cada aba (`switch`, `chd`, `xiso`, `rvz`, `ciso`, `7z`); vazio = ao lado de cada arquivo. O `output_dir` das versões anteriores vale para as abas que ainda não têm uma.
- `last_dir`: última pasta aberta nos diálogos.
- `format`: `nsz` (comprimido) ou `nsp`.
- `level`: `fast`, `normal` ou `max`.
- `skip_verify`: `true` desliga a verificação após gravar.
- `apply_update`: `true` aplica a atualização no jogo base (só emuladores).
- `tab`: aba mostrada por último (`switch`, `chd`, `xiso`, `rvz`, `ciso` ou `7z`).
- `chd_no_m3u`: `true` desliga as playlists `.m3u` de jogos com vários discos.
- `ciso_format`: formato das ISOs comprimidas, `cso` (padrão) ou `zso`.
- `rvz_chunk_kib`: tamanho do bloco do RVZ em KiB (`128`, `512`, `1024` ou `2048`; padrão `128`).
- `wii_key_path`: arquivo da chave comum do Wii escolhido em **Procurar…** (a pasta `keys` também é procurada).
- `sevenzip_exts`: extensões marcadas (`true`) ou desmarcadas (`false`) na aba ROMs; as outras seguem o padrão.
- `sevenzip_delete`: `true` apaga os originais depois que o 7z deles é verificado.
- `show_hidden`: `true` mostra arquivos e pastas ocultos nas janelas de arquivo do Tk (Linux).
- `retroachievements_key`: a sua chave de API do RetroAchievements, para verificar ROMs; o arquivo só pode ser lido pelo seu usuário (Linux).
- `window`, `maximized`, `columns`, `divider`: layout da janela na última execução (gravados ao fechar; apague-os para voltar ao padrão).

Se a pasta do programa não permitir escrita (por exemplo, instalado em `/usr/bin`), o arquivo vai para a pasta de configuração do usuário (`~/.config/gameforge/` no Linux, `%AppData%\gameforge\` no Windows). A pasta `keys/` também é criada automaticamente se não existir.

## Compilação

Requer só o Go 1.27+: o Tk é usado sem CGO (as bibliotecas Tcl/Tk vão embutidas no executável e são extraídas na primeira execução para a pasta de cache do usuário). Não precisa de compilador C, e dá para compilar para Linux a partir do Windows e vice-versa.

### Windows

```powershell
./build.ps1
```

ou à mão, com a flag que evita a janela de console:

```bash
CGO_ENABLED=0 go build -ldflags "-H windowsgui -s -w" -o gameforge.exe .
```

Se o executável for gerado sem essa flag (por exemplo, um `go build` simples), o app fecha a janela de console logo ao abrir; ela ainda pode piscar por um instante.

O ícone do `.exe` vem dos arquivos `rsrc_windows_*.syso`, que o `go build` inclui sozinho. O desenho fica em `internal/icon` (a janela também o usa); se ele mudar, gere os `.syso` de novo com `go run ./tools/mkicon`.

### Linux

```bash
./build.sh
```

ou `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o gameforge .` em qualquer sistema. Para rodar, o Linux precisa de um ambiente gráfico X11 (ou XWayland) com as bibliotecas `libX11`, `libXss`, `libXft` e `libfontconfig`, que qualquer desktop já instala.

### Testes

```bash
go test ./internal/...
```

Os testes geram NSPs sintéticos com chaves aleatórias (NCA com cabeçalho AES-XTS, seção AES-CTR via key area e via title key/ticket) e verificam a análise, a mesclagem e a compressão NCZ (incluindo seções BKTR de atualizações) com ida e volta byte a byte.

A conversão CHD tem testes de ida e volta com imagens sintéticas e um teste de compatibilidade com o `chdman` do MAME, que só roda quando a variável `CHDMAN` aponta para o executável (o `chdman.exe` vem no pacote do MAME para Windows):

```bash
CHDMAN=/caminho/para/chdman.exe go test ./internal/chd -run Chdman -v
```

Ele converte as mesmas imagens com o app e com o `chdman`, roda `chdman verify` no CHD do app, compara o SHA-1 dos dados e o SHA-1 geral, confere que `chdman extractcd`/`extractdvd` devolve imagens idênticas e que o app lê os CHDs do `chdman` (codecs `cdfl`, `huff` e `flac`). Há também um teste de ida e volta do codificador/decodificador FLAC.

A conversão RVZ é testada com um disco de GameCube sintético (arquivos, tabela de arquivos e preenchimento do gerador com uma semente por bloco de 32 KiB) e um disco de Wii sintético (tabela de partições, ticket com a chave do título criptografada por uma chave comum de teste, dados com FST, arquivos e preenchimento, árvore de hashes com algumas hashes alteradas para exigir exceções, criptografia AES): a recuperação da semente em várias posições, a ida e volta byte a byte, o tamanho final e os erros sem chave e com chave errada. Um teste de compatibilidade com o Dolphin roda quando a variável `DOLPHINTOOL` aponta para o `DolphinTool.exe` (que vem no Dolphin para Windows):

```bash
DOLPHINTOOL=/caminho/para/DolphinTool.exe go test ./internal/rvz -run Dolphin -v
```

Ele confere, para GameCube e Wii, que o Dolphin devolve a ISO original a partir do RVZ do app e que o app lê o RVZ feito pelo Dolphin. Também são testados o cabeçalho do RVZ (cabeçalho do disco e tamanho da imagem) e o CRC da imagem descompactada, a renomeação de um RVZ de GameCube sintético entre duas revisões, a leitura de qualquer parte de um RVZ de GameCube sem descompactar o resto, e o banner de um disco sintético (cores nos dois formatos de pixel, um arquivo de mesmo nome numa subpasta), igual a partir do ISO e do RVZ.

O gravador e o leitor de 7z são testados com ida e volta byte a byte (dados compressíveis e aleatórios, arquivo vazio, pasta e nome com acentos, arquivo único, só arquivos vazios), com um arquivo corrompido que precisa falhar na verificação e com a conversão de uma pasta de ROMs e um ZIP de arcade sintéticos (quais extensões entram na lista, os arquivos dentro, quais originais são removidos). Um teste de compatibilidade com o 7-Zip roda quando a variável `SEVENZIP` aponta para o `7z.exe`:

```bash
SEVENZIP=/caminho/para/7z.exe go test ./internal/sevenzip . -run "7Zip|SevenZip" -v
```

Ele confere que o `7z t` aceita os arquivos, que o `7z x` extrai os arquivos originais e que o app lê arquivos feitos pelo 7-Zip, inclusive a lista de arquivos dos arquivos padrão dele, com cabeçalho comprimido.

A renomeação pelo No-Intro é testada com uma base de exemplo (entradas com e sem cabeçalho, download com um segundo download sem mudanças, o arquivo de cache), com ROMs diferentes da base (outro cabeçalho iNES, ordens de bytes `.v64` e `.n64`), com uma ROM solta, um ZIP, um 7z e um ZIP de arcade com vários arquivos, com uma lista de Neo Geo (nomes dos jogos, títulos unidos), e com as bases reais quando `NOINTRO_DATS` aponta para uma pasta com elas.

A verificação no RetroAchievements é testada com as regras de hash de cada tipo de console (cabeçalhos removidos, ordens de bytes do Nintendo 64, as partes de uma ROM de DS com e sem cabeçalho SuperCard), com uma API falsa (listas de jogos com hashes, chave recusada, a chave nunca aparecendo nos erros, o arquivo de cache) e com uma ROM solta, um ZIP, um 7z, uma ROM de NES com cabeçalho e um ZIP de arcade. Em 46 ROMs reais de GBA, Mega Drive, SNES e PC Engine (em ZIPs e 7z, uma ROM de PC Engine com cabeçalho de 512 bytes), os hashes foram iguais aos MD5 das bases do No-Intro, que é o que o RetroAchievements usa nesses consoles; depois, a verificação de cartuchos foi confirmada no próprio RetroAchievements. Os hashes de disco são testados com discos sintéticos (PS1 com cabeçalho PS-X EXE e com PSX.EXE, PS2, arquivos de PSP em pastas, um arquivo de boot de Dreamcast além da faixa 3, cabeçalhos de Saturn e Sega CD) e foram calculados em 20 CHDs reais (GD-ROMs de Dreamcast com até 12 faixas, PS1, PS2, PSP, Saturn) sem erros, e confirmados no próprio RetroAchievements com jogos reais de Dreamcast, PS1 e PSP.

As imagens dos jogos são testadas com dados sintéticos: um banner de DS (blocos, paleta, transparência, lido um byte por vez), texturas PVR de Dreamcast (twiddled, com mipmaps, comprimidas em VQ, um rótulo grande reduzido) e imagens de título de Xbox num XBE (DXT1 e 32 bits swizzled). Em arquivos reais, os ícones de quatro ROMs de DS (soltas e num 7z), o rótulo de um CHD de Dreamcast e as imagens de título de três XISOs de Xbox (DXT1, 128 × 128) foram conferidos a olho.

A compressão CSO/ZSO é testada com ida e volta byte a byte (dados compressíveis, aleatórios e um último bloco incompleto), com um índice deslocado com blocos alinhados, e com o decodificador LZ4 do app conferido contra a biblioteca LZ4; um CSO gerado também foi lido por um decodificador independente em Python (zlib), que chegou à imagem original.

As informações do jogo são testadas com discos sintéticos de cada plataforma (ISO de PSP com `PARAM.SFO` e ícone, `SYSTEM.CNF` de PS1 e PS2, cabeçalhos de Saturn, Sega CD e Dreamcast, cabeçalho de GameCube), lidas da ISO, do CUE/BIN ou do GDI e dos CHDs feitos a partir deles (CD, DVD e GD-ROM, inclusive um multi-cue do Redump); com `CHDMAN` definido, também de CHDs feitos pelo `chdman`.

A renomeação pelo Redump é testada com uma base de exemplo (entradas repetidas, várias entradas por código separadas pelo tamanho e pelo CRC, vários códigos por jogo, códigos com sufixo, download com um segundo download sem mudanças, o arquivo de cache), com as bases reais quando `REDUMP_DATS` aponta para uma pasta com elas, e com playlists `.m3u` atualizadas depois de renomear. O tamanho e o CRC das faixas lidas de CHDs foram conferidos com o Redump em discos reais (faixa 1 de PS1, faixa 3 de Dreamcast).

A conversão XISO é testada com imagens de Xbox sintéticas (uma imagem de disco com a partição do jogo deslocada e outra já em XISO, com preenchimento aleatório, pasta vazia, arquivo vazio e uma tabela de diretório maior que um setor): o resultado precisa ter os mesmos arquivos com o mesmo conteúdo, e regravar uma XISO gerada precisa dar os mesmos bytes.

## Limitações

- Somente NCA3 (todo o conteúdo atual). Tickets personalizados (vinculados ao console) não são decifrados; nesse caso o app usa o nome do arquivo, e a mesclagem funciona do mesmo jeito.
- Não gera XCI e não lê XCZ (XCI comprimido).
- NCAs com criptografia que os instaladores não sabem refazer (XTS, tipos novos) ou seções não contíguas ficam sem compressão.
- Arrastar arquivos para a janela só funciona no Windows: o Tk 9.0 não tem drag and drop, e o app o implementa pela API do Windows. No Linux, use os botões.

## Aviso

Use apenas com jogos que você comprou e com chaves extraídas do seu próprio console. O `prod.keys` nunca é copiado nem enviado para lugar algum.

## Licença

[MIT](LICENSE). As bibliotecas usadas têm as suas próprias licenças, reproduzidas em [THIRD_PARTY_LICENSES.txt](THIRD_PARTY_LICENSES.txt) (MIT, BSD, Apache 2.0, ISC e MPL 2.0), todas compatíveis. As bases do Redump, baixadas pelo app quando você renomeia imagens de disco, são © Redump e libretro, sob CC BY-SA 4.0, e as do No-Intro, baixadas quando você renomeia ROMs, são © No-Intro, publicadas pelo libretro; elas não fazem parte deste repositório.
