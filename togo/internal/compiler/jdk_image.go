package compiler

// Read compiled classes out of a provisioned JDK's jmods/ directory, on demand.
// Port of src/compiler/jdkImage.ts. A real JDK gives the type checker the full
// standard-library API surface that the hand-written jdkstub.go only
// approximates. We only ever touch the jmods of a JDK cappu itself provisioned
// (config "jdk"); a .jmod is an ordinary zip with a 4-byte magic header ("JM" +
// version), so the existing zip reader reads it once the header is stripped, and
// classes live under a "classes/" prefix.
//
// Everything here is lazy: nothing is read until the first JDK type is actually
// resolved (so startup is untouched), and only the modules a project touches are
// held in memory.

import (
	"os"
	"path/filepath"
	"strings"
)

// JdkImage reads compiled classes from a provisioned JDK's jmods/.
type JdkImage struct {
	jmodDir   string
	jmodFiles []string
	// package ("java/util") -> module file ("java.base.jmod"). Built once, on the
	// first miss, then retained (strings only).
	packageToModule map[string]string
	// module file -> (outer binary name -> its family: the class itself and its
	// Outer$* nested classes, in archive order). Indexed once per module so each
	// lookup is O(family) instead of a scan over all ~6000 java.base entries.
	// Only modules we actually read classes from are kept.
	// ponytail: keeps the data of modules we serve classes from (typically just
	// java.base); if resident memory ever matters, switch to seek-based reads.
	openModules map[string]map[string][]ZipEntry
}

// readJmodEntries reads a .jmod file and returns its zip entries, or nil if not
// a jmod. The .jmod magic is 0x4A 0x4D ("JM") then a 2-byte version; the rest is
// a zip, which archive/zip reads from the stripped slice.
func readJmodEntries(path string) []ZipEntry {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 4 || data[0] != 0x4A || data[1] != 0x4D {
		return nil
	}
	return ReadZipEntries(data[4:])
}

// packageOfEntry returns the package of a "classes/java/util/List.class" entry,
// as "java/util" (empty string for a class in the default package). ok=false
// when the entry is not a class under classes/.
func packageOfEntry(name string) (string, bool) {
	if !strings.HasPrefix(name, "classes/") || !strings.HasSuffix(name, ".class") {
		return "", false
	}
	path := name[len("classes/") : len(name)-len(".class")]
	slash := strings.LastIndexByte(path, '/')
	if slash < 0 {
		return "", true
	}
	return path[:slash], true
}

// NewJdkImage returns a reader over the jmods/ of the JDK at jdkHome, or nil when
// there are no jmods (e.g. a JRE, or a stripped image) - the caller keeps the stub.
func NewJdkImage(jdkHome string) *JdkImage {
	jmodDir := filepath.Join(jdkHome, "jmods")
	dirEntries, err := os.ReadDir(jmodDir)
	if err != nil {
		return nil
	}
	var jmodFiles []string
	for _, e := range dirEntries {
		if strings.HasSuffix(e.Name(), ".jmod") {
			jmodFiles = append(jmodFiles, e.Name())
		}
	}
	if len(jmodFiles) == 0 {
		return nil
	}
	// java.base first: it holds java.lang/util/io/... which is almost everything a
	// project resolves, so the package scan usually stops after one module.
	for i, f := range jmodFiles {
		if f == "java.base.jmod" {
			jmodFiles[0], jmodFiles[i] = jmodFiles[i], jmodFiles[0]
			break
		}
	}
	return &JdkImage{jmodDir: jmodDir, jmodFiles: jmodFiles, openModules: map[string]map[string][]ZipEntry{}}
}

func (img *JdkImage) buildPackageMap() {
	img.packageToModule = map[string]string{}
	for _, modFile := range img.jmodFiles {
		for _, entry := range readJmodEntries(filepath.Join(img.jmodDir, modFile)) {
			if pkg, ok := packageOfEntry(entry.Name); ok {
				if _, has := img.packageToModule[pkg]; !has {
					img.packageToModule[pkg] = modFile // first module wins
				}
			}
		}
		// data for this module is dropped here unless entriesFor later reopens it
	}
}

// classPathOfEntry returns "java/util/Map$Entry" for "classes/java/util/Map$Entry.class".
func classPathOfEntry(name string) (string, bool) {
	if !strings.HasPrefix(name, "classes/") || !strings.HasSuffix(name, ".class") {
		return "", false
	}
	return name[len("classes/") : len(name)-len(".class")], true
}

func (img *JdkImage) familiesFor(modFile string) map[string][]ZipEntry {
	if cached, ok := img.openModules[modFile]; ok {
		return cached
	}
	families := map[string][]ZipEntry{}
	for _, entry := range readJmodEntries(filepath.Join(img.jmodDir, modFile)) {
		path, ok := classPathOfEntry(entry.Name)
		if !ok {
			continue
		}
		// The family key is the outermost class: everything before the first '$'
		// of the simple name ("java/util/Map$Entry" files under "java/util/Map").
		outer := path
		slash := strings.LastIndexByte(path, '/')
		if dollar := strings.IndexByte(path[slash+1:], '$'); dollar >= 0 {
			outer = path[:slash+1+dollar]
		}
		families[outer] = append(families[outer], entry)
	}
	img.openModules[modFile] = families
	return families
}

// ReadClassFamily returns the compiled bytes of binaryName (e.g. "java/util/List")
// plus every binaryName$* nested class in the same module, so a stub built from
// them folds the nested types in. nil when the class is not in the image.
func (img *JdkImage) ReadClassFamily(binaryName string) [][]byte {
	if img.packageToModule == nil {
		img.buildPackageMap()
	}
	pkg := ""
	if slash := strings.LastIndexByte(binaryName, '/'); slash >= 0 {
		pkg = binaryName[:slash]
	}
	modFile, ok := img.packageToModule[pkg]
	if !ok {
		return nil
	}
	entries := img.familiesFor(modFile)[binaryName]
	outerName := "classes/" + binaryName + ".class"
	nestedPrefix := "classes/" + binaryName + "$"
	var outer []byte
	var nested [][]byte
	for _, entry := range entries {
		switch {
		case entry.Name == outerName:
			outer = entry.Read()
		case strings.HasPrefix(entry.Name, nestedPrefix) && strings.HasSuffix(entry.Name, ".class"):
			nested = append(nested, entry.Read())
		}
	}
	if outer == nil {
		return nil
	}
	return append([][]byte{outer}, nested...)
}
