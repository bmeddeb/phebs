package com.google.devtools.build.lib.bazel.repository.downloader;

import com.google.auth.Credentials;
import com.google.devtools.build.lib.bazel.repository.RepositoryOptions;
import com.google.devtools.build.lib.bazel.repository.cache.DownloadCache;
import com.google.devtools.build.lib.bazel.repository.cache.DownloadCache.KeyType;
import com.google.devtools.build.lib.bazel.repository.cache.DownloadCacheHitEvent;
import com.google.devtools.build.lib.bazel.repository.decompressor.DecompressorDescriptor;
import com.google.devtools.build.lib.bazel.repository.decompressor.DecompressorValue;
import com.google.devtools.build.lib.events.Event;
import com.google.devtools.build.lib.events.ExtendedEventHandler;
import com.google.devtools.build.lib.vfs.DigestHashFunction;
import com.google.devtools.build.lib.vfs.FileSystemUtils;
import com.google.devtools.build.lib.vfs.Path;
import com.google.devtools.build.lib.vfs.inmemoryfs.InMemoryFileSystem;
import com.google.devtools.common.options.OptionsParser;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.StringReader;
import java.net.URL;
import java.net.URLConnection;
import java.net.URLStreamHandler;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Phaser;
import java.util.zip.GZIPOutputStream;
import org.apache.commons.compress.archivers.tar.TarArchiveEntry;
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream;

/** Calls the unchanged downloader classes embedded in the admitted Bazel binary. */
public final class DownloaderProof {
  private static final InMemoryFileSystem FS = new InMemoryFileSystem(DigestHashFunction.SHA256);
  private static final DownloadCache CACHE = new DownloadCache();
  private static final String ARCHIVE = "https://github.com/owned/fixture/releases/download/v1/fixture.tar.gz";
  private static final String REGISTRY = "https://bcr.bazel.build/modules/owned/1/source.json";
  private static final String CANONICAL = ARCHIVE;
  private static final String SENTINEL = "COUNTED_NETWORK_BOUNDARY";
  private static int archiveCalls;
  private static int registryCalls;
  private static int escapedNetwork;
  private static int outputID;
  private static final List<DownloadCacheHitEvent> HITS = new ArrayList<>();
  private static final ExtendedEventHandler EVENTS = new ExtendedEventHandler() {
    public void handle(Event event) {}
    public void post(Postable event) {
      if (event instanceof DownloadCacheHitEvent hit) HITS.add(hit);
    }
  };
  private static final Downloader NETWORK = (urls, headers, credentials, checksum, canonicalId,
      destination, eventHandler, environment, type, context) -> {
    archiveCalls++;
    throw new IOException(SENTINEL);
  };
  private static final HttpDownloader REGISTRY_NETWORK = new HttpDownloader() {
    @Override public byte[] downloadAndReadOneUrl(URL url, Credentials credentials,
        Optional<Checksum> checksum, ExtendedEventHandler events, Map<String, String> environment)
        throws IOException {
      registryCalls++;
      throw new IOException(SENTINEL);
    }
  };

  @FunctionalInterface private interface Action { void run() throws Exception; }

  private static void check(boolean condition, String message) {
    if (!condition) throw new AssertionError(message);
  }

  private static void refused(String text, Action action) throws Exception {
    try {
      action.run();
      throw new AssertionError("expected refusal: " + text);
    } catch (IOException exception) {
      check(exception.getMessage().contains(text), "wrong refusal: " + exception);
    }
  }

  private static String sha(byte[] value) {
    return KeyType.SHA256.newHasher().putBytes(value).hash().toString();
  }

  private static Optional<Checksum> checksum(byte[] value) throws Exception {
    return Optional.of(Checksum.fromString(KeyType.SHA256, sha(value)));
  }

  private static DownloadManager manager(String config, boolean disable) throws Exception {
    DownloadManager result = new DownloadManager(CACHE, NETWORK, REGISTRY_NETWORK, EVENTS);
    result.setDisableDownload(disable);
    result.setDistdir(List.of(FS.getPath("/inputs/tools/cache/distdir")));
    result.setUrlRewriter(new UrlRewriter(s -> {}, "owned config", new StringReader(config)));
    return result;
  }

  private static Path download(DownloadManager manager, String url, Optional<Checksum> checksum,
      String canonical, Optional<String> type) throws Exception {
    Path output = FS.getPath("/outputs/case" + outputID++);
    try (ExecutorService executor = Executors.newSingleThreadExecutor()) {
      return manager.finalizeDownload(manager.startDownload(executor, List.of(new URL(url)),
          Map.of(), Map.of(), checksum, canonical, type, output, Map.of(), "owned proof",
          new Phaser(), false));
    }
  }

  private static byte[] archive(byte[] content) throws Exception {
    ByteArrayOutputStream bytes = new ByteArrayOutputStream();
    try (TarArchiveOutputStream tar = new TarArchiveOutputStream(new GZIPOutputStream(bytes))) {
      TarArchiveEntry entry = new TarArchiveEntry("member.txt");
      entry.setSize(content.length);
      entry.setModTime(0);
      tar.putArchiveEntry(entry);
      tar.write(content);
      tar.closeArchiveEntry();
    }
    return bytes.toByteArray();
  }

  public static void main(String[] args) throws Exception {
    check(args.length == 1, "config argument required");
    String config = Files.readString(java.nio.file.Path.of(args[0]));
    check(config.equals("block bcr.bazel.build\n"), "unexpected admitted config");
    // Even an accidental call past the counted interfaces cannot open an HTTP socket or resolve DNS.
    URL.setURLStreamHandlerFactory(protocol -> {
      if (!protocol.equals("http") && !protocol.equals("https")) return null;
      return new URLStreamHandler() {
        @Override protected URLConnection openConnection(URL url) {
          escapedNetwork++;
          throw new AssertionError("unexpected HTTP connection attempt");
        }
      };
    });
    CACHE.setPath(FS.getPath("/cache"));
    CACHE.setHardlink(false);
    check(!FS.getPath("/inputs/tools/cache/distdir").exists(), "configured distdir unexpectedly exists");
    byte[] member = "owned downloader proof\n".getBytes(StandardCharsets.UTF_8);
    byte[] tar = archive(member);
    Path input = FS.getPath("/input.tar.gz");
    FileSystemUtils.writeContent(input, tar);
    CACHE.put(sha(tar), input, KeyType.SHA256, CANONICAL);
    Optional<Checksum> present = checksum(tar);
    Optional<Checksum> missing = checksum("missing".getBytes(StandardCharsets.UTF_8));
    DownloadManager closed = manager(config, true);

    OptionsParser options = OptionsParser.builder().optionsClasses(RepositoryOptions.class).build();
    options.parse("--repository_disable_download", "--downloader_config=/inputs/tools/cache/downloader.cfg");
    RepositoryOptions parsed = options.getOptions(RepositoryOptions.class);
    check(parsed.disableDownload, "actual repository option did not disable downloads");
    check(parsed.downloaderConfig.toString().equals("/inputs/tools/cache/downloader.cfg"), "config option changed");

    Path hit = download(closed, ARCHIVE, present, CANONICAL, Optional.of(""));
    check(hit.getBaseName().equals("fixture.tar.gz"), "archive suffix lost");
    check(Arrays.equals(tar, FileSystemUtils.readContent(hit)), "archive cache content changed");
    check(DownloadCache.getChecksum(KeyType.SHA256, hit).equals(sha(tar)), "archive digest changed");
    Path extracted = FS.getPath("/extracted");
    DecompressorValue.decompress(DecompressorDescriptor.builder().setContext("owned proof")
        .setArchivePath(hit).setDestinationPath(extracted).build());
    check(Arrays.equals(member, FileSystemUtils.readContent(extracted.getRelative("member.txt"))), "extraction changed member");
    check(archiveCalls == 0 && registryCalls == 0, "cache hit reached network");
    Path oldHit = download(manager("block *\n", true), ARCHIVE, present, CANONICAL, Optional.of(""));
    check(oldHit.getBaseName().equals("cacheprobe"), "old defect control did not lose suffix");
    try {
      DecompressorValue.decompress(DecompressorDescriptor.builder().setContext("old defect")
          .setArchivePath(oldHit).setDestinationPath(FS.getPath("/old-extracted")).build());
      throw new AssertionError("old extensionless archive unexpectedly extracted");
    } catch (com.google.devtools.build.lib.bazel.repository.RepositoryFunctionException expected) {
      check(expected.getCause().getMessage().contains("Expected a file with a"), "wrong old extraction refusal: " + expected);
    }

    for (Optional<String> type : List.of(Optional.of(""), Optional.<String>empty())) {
      refused("download is disabled", () -> download(closed, ARCHIVE, missing, CANONICAL, type));
      refused("download is disabled", () -> download(closed, ARCHIVE, Optional.empty(), CANONICAL, type));
      refused("download is disabled", () -> download(closed, ARCHIVE, present, "wrong canonical ID", type));
    }
    Path cachedArchive = FS.getPath("/cache/sha256/" + sha(tar) + "/file");
    FileSystemUtils.writeContent(cachedArchive, member);
    refused("download is disabled", () -> download(closed, ARCHIVE, present, CANONICAL, Optional.of("")));
    check(archiveCalls == 0, "archive/plain/corrupt-cache miss attempted network");
    refused(SENTINEL, () -> download(manager(config, false), ARCHIVE, missing, CANONICAL, Optional.of("")));
    check(archiveCalls == 1, "archive positive network-counter control failed");
    archiveCalls = 0;

    byte[] metadata = "{\"owned\":true}\n".getBytes(StandardCharsets.UTF_8);
    CACHE.put(sha(metadata), metadata, KeyType.SHA256);
    int beforeHit = HITS.size();
    check(Arrays.equals(metadata, closed.downloadAndReadOneUrlForBzlmod(new URL(REGISTRY), Map.of(), checksum(metadata))), "registry cache content changed");
    check(HITS.size() == beforeHit + 1, "registry cache-hit event missing");
    DownloadCacheHitEvent registryHit = HITS.getLast();
    check(registryHit.getUrl().toString().equals(REGISTRY), "registry URL authority changed");
    check(registryHit.getFileHash().equals(sha(metadata)), "registry hash authority changed");
    for (String registry : List.of(REGISTRY, "https://child.bcr.bazel.build/source.json")) {
      for (Optional<Checksum> sum : List.of(missing, Optional.<Checksum>empty())) {
        refused("URL rewriter blocked all URLs", () -> closed.downloadAndReadOneUrlForBzlmod(new URL(registry), Map.of(), sum));
      }
    }
    check(registryCalls == 0, "registry miss attempted network");
    refused(SENTINEL, () -> manager("", true).downloadAndReadOneUrlForBzlmod(new URL(REGISTRY), Map.of(), missing));
    check(registryCalls == 1, "registry positive network-counter control failed");
    registryCalls = 0;

    String patch = "https://bcr.bazel.build/modules/owned/1/patches/owned.patch";
    Path patchInput = FS.getPath("/input.patch");
    FileSystemUtils.writeContent(patchInput, member);
    CACHE.put(sha(member), patchInput, KeyType.SHA256, CANONICAL);
    Path patchHit = download(closed, patch, checksum(member), CANONICAL, Optional.empty());
    check(patchHit.getBaseName().startsWith("case"), "plain download replaced explicit output path");
    check(Arrays.equals(member, FileSystemUtils.readContent(patchHit)), "blocked-host patch cache hit changed");
    refused("Cache miss and no url specified", () -> download(closed, patch, missing, CANONICAL, Optional.empty()));
    refused("download is disabled", () -> download(closed, patch, Optional.empty(), CANONICAL, Optional.empty()));
    check(archiveCalls == 0 && registryCalls == 0 && escapedNetwork == 0, "closed paths attempted network");
    System.out.println("archive_suffix_extract=PASS old_policy_defect=PASS archive_plain_misses=PASS canonical_id=PASS corrupt_cache=PASS configured_absent_distdir=PASS registry_cache_identity=PASS registry_known_unknown_misses=PASS blocked_patch_cache=PASS network_counter_controls=PASS option_binding=PASS network_attempts=0");
    System.out.println("NATIVE_DOWNLOADER_HOST_PROOF_PASS");
  }
}
