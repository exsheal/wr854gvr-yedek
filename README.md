# WR854GVR Yedek Al

Aidata **WR854GVR** (Realtek RTL9607F / "Elnath") stok firmware'i için **salt-okunur NAND yedekleme** aracı.

Windows'ta çalışan tek dosyalık bir konsol programıdır. Cihazın web arayüzüne giriş yapar, stok firmware'in kendi yükleme ucunu kullanarak cihazda root olarak bir `nanddump` betiği çalıştırır ve NAND dökümünü ağ üzerinden bu bilgisayara aktarır.

> **Bu araç cihazın flash'ına yazmaz.** `nanddump` salt-okunur çalışır, boot environment değişmez, cihaz yeniden başlatılmaz. Ayrıntısı [Güvenlik](#güvenlik) bölümünde.

---

## Neden var?

WR854GVR'de UART erişimi olmayan bir kullanıcı, OpenWrt'ye geçmeden önce stok firmware'in yedeğini alamıyordu. Cihazın stok yükleme yolu (`/boaform/admin/formUpload`) firmware imajını yalnızca `tar tf <dosya> md5.txt` ile kontrol ediyor; imza, model veya sürüm doğrulaması yok. Bu, cihaz sahibinin kendi donanımının yedeğini almasını mümkün kılıyor — bu araç tam olarak bunu yapıyor ve başka hiçbir şey yapmıyor.

Bu araç **yalnızca sahibi olduğunuz cihazlarda** kullanılmak içindir.

---

## Kullanım

```
WR854GVR-Yedek.exe
```

Program sırayla şunları sorar:

```
Cihaz IP adresi [192.168.1.1]:
Web arayüzü kullanıcı adı [admin]:
Web arayüzü şifresi:
Yedeğin kaydedileceği klasör [...\Desktop\WR854GVR-yedek-20261109-1200]:
```

Sonra bağlantıları kontrol eder (cihaz yanıtı, giriş, yükleme sayfası, yerel adres, portlar), onay ister ve yedeği alır.

### Çıktı

| Dosya | Boyut | İçerik |
|---|---|---|
| `mtd0_boot.bin` | 2.1 MiB | bootloader |
| `mtd1_env.bin` | 272 KiB | U-Boot environment |
| `mtd2_env2.bin` | 272 KiB | U-Boot environment (yedek) |
| `mtd3_static_conf.bin` | 272 KiB | statik yapılandırma |
| `mtd4_ubi_device.bin` | 533 MiB | tüm UBI (kernel, rootfs, apps, config, packets) |
| `md5sums.txt` | — | doğrulama |

Dökümler **OOB baytları dahil** alınır (`nanddump -o`), bu yüzden dosya boyutları ham bölüm boyutundan büyüktür. Bu bilinçli bir tercih: OOB, NAND'in hata düzeltme ve kötü blok bilgisini taşır.

---

## Gereksinimler

**Windows tarafı**

- Windows 10 veya 11, 64-bit
- Ek DLL / runtime **yok** — tek bağımlılık `kernel32.dll`
- Yönetici hakkı gerekmez (portlar 1024 üstünde)
- ~600 MB boş disk alanı

**Ağ**

- Bilgisayar cihazla **aynı L2 ağında** olmalı. Cihaz yedeği göndermek için size **geri bağlanır** (port 9101-9105). Ayrı VLAN / NAT arkasında çalışmaz.
- Windows güvenlik duvarı ilk çalıştırmada izin ister — **"Özel ağlar" işaretli olarak İzin Ver**. Bu izin verilmezse dosyalar boş kalır.

**Cihaz tarafı**

Hiçbir şey yüklemek gerekmez. Kullanılan `nanddump`, `nc` (busybox), `tar`, `md5sum` stok firmware'de zaten mevcuttur.

---

## Güvenlik

Bu araç, stok firmware'in firmware yükleme yolunu kullanır:

```
POST /boaform/admin/formUpload
  -> libmib.so :: cmd_upload_split
  -> SysV mesaj kuyruğu (ftok("/bin/init", 0x73))
  -> configd
  -> system("/etc/scripts/fwu_starter_split.sh <bank> <img>")
  -> sh fwu_split.sh          (root olarak)
```

Gönderilen betik **bilerek `exit 1` ile biter.** Bunun nedeni stok `configd`'nin kodunda:

```c
local_4 = system(acStack_170);
if (local_4 == 0) {                      /* <-- yalnızca script 0 dönerse */
    mib_set(0xfc1, ...);
    if (-1 < (int)param_4) {
        rtk_env_set("sw_updater", ...);   /* boot env yazımı */
        rtk_env_set("sw_tryactive", ...);
    }
    if (0 < (int)param_4) {
        puts("The system is restarting ...");   /* reboot */
        ...
    }
}
```

`exit 1` ile bu dal hiç çalışmaz: **ne boot env yazılır ne cihaz yeniden başlar.** Araçta `flash`, `nandwrite`, `ubiupdatevol`, `mtd write` çağrısı yoktur — yalnızca `nanddump`.

Cihazın bu isteğe verdiği `Geçersiz resim dosyası` yanıtı **beklenen** sonuçtur; döküm yine de alınmış olur.

### Bilinen doğrulama zayıflıkları

Bu araç, stok firmware'in doğrulama eksikliğinden yararlanır. Kendi cihazınızı yedeklemek için kullanışlıdır, ama aynı zamanda şunu gösterir:

- Yükleme yolunda **kriptografik imza kontrolü yoktur**; tek test `tar tf <dosya> md5.txt`.
- Sonraki md5 karşılaştırması, saldırganın **aynı tarball içinde** sağladığı dosyaya karşı yapılır — yani gerçek bir bütünlük garantisi değil, kendi kendini doğrulamadır.
- Erişim, fabrika varsayılanı `admin/admin` ile açıktır.
- HTTP Basic kimlik doğrulaması **şifreyi doğrulamaz** (herhangi bir parola ile 200 döner); gerçek kontrol `formLogin` POST'undadır.

Bu bir sorumlu açıklama değil, bir gözlemdir. Cihaz üreticisine bildirilmesi önerilir.

---

## Derleme

Go 1.21+ gerekir. Harici bağımlılık yoktur.

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o WR854GVR-Yedek.exe .
```

Linux/macOS'ta aynı kod yerel olarak da derlenir (test için):

```bash
go build -o wr854-backup .
```

---

## Sınırlar

- Yalnızca **WR854GVR** için yazılmıştır; bölüm haritası (`/proc/mtd`) bu modele sabittir.
- Stok firmware `V4.0.18_260920` üzerinde test edilmiştir. Farklı sürümlerde `configd` davranışı değişebilir.
- Cihaz yedeği alınırken UBI canlı okunur; `ubi_Config`, `ubi_packets` gibi bölümler o sırada yazılıyor olabilir. Bu yüzden iki farklı koşunun `mtd4` çıktısı birbirini tutmayabilir — küçük bölümler (`mtd0`-`mtd3`) tutarlıdır.

---

## Lisans

MIT — bkz. [LICENSE](LICENSE).
