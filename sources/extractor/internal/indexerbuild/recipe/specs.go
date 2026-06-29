package recipe

import "github.com/mohammad-safakhou/diffmind/internal/langdetect"

// languageSpec describes which versions we know how to build for a
// language and the default to use when detection produces nothing.
//
// Versions is the canonical set users see in base-image tags; e.g.
// for Java we cache "8", "11", "17", "21". Default is what we
// install when detection produces no version marker.
type languageSpec struct {
	Versions []string
	Default  string
}

// supportedLanguages enumerates every language the recipe can
// generate a base image for. Languages NOT here are silently
// skipped — Generate proceeds with whatever IS supported, and the
// composite simply omits the missing language's indexer.
//
// Adding a new language requires:
//   - An entry here.
//   - A baseDockerfileFor() branch (templates.go).
//   - A compositeDockerfile() COPY block (templates.go).
//   - The corresponding indexer binary in the wrapper.
var supportedLanguages = map[langdetect.Language]languageSpec{
	langdetect.LangJava: {
		// Keep versions in ascending order; pickVersion selects the closest
		// match and falls back to Default when the requested version is not
		// in the list or newer than what we know about.
		// When a project targets a Java version newer than our highest entry
		// (e.g. Java 25 while our max is 21) we still select the highest
		// available version rather than falling all the way back to the
		// default. The newer language features may produce compile errors
		// inside the container, but those are surfaced clearly in the error
		// field rather than a silent "wrong JDK" failure.
		Versions: []string{"8", "11", "17", "21", "25"},
		Default:  "21",
	},
	langdetect.LangKotlin: {
		// We co-install Kotlin on top of the Java base image
		// since semanticdb-kotlinc runs under javac. The
		// "version" here is the Kotlin compiler version, not
		// the JDK.
		Versions: []string{"1.9", "2.0"},
		Default:  "1.9",
	},
	langdetect.LangTypeScript: {
		Versions: []string{"18", "20", "22"},
		Default:  "20",
	},
	langdetect.LangJavaScript: {
		Versions: []string{"18", "20", "22"},
		Default:  "20",
	},
	langdetect.LangPython: {
		Versions: []string{"3.10", "3.11", "3.12"},
		Default:  "3.12",
	},
	langdetect.LangGo: {
		Versions: []string{"1.21", "1.22", "1.23"},
		Default:  "1.22",
	},
	langdetect.LangRuby: {
		Versions: []string{"3.2", "3.3"},
		Default:  "3.3",
	},
	langdetect.LangCSharp: {
		Versions: []string{"6.0", "7.0", "8.0"},
		Default:  "8.0",
	},
}
