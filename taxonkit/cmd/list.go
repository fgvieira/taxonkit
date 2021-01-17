// Copyright © 2016-2021 Wei Shen <shenwei356@gmail.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"bufio"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/shenwei356/xopen"
	"github.com/spf13/cobra"
)

// listCmd represents the fx2tab command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List taxonomic tree of given taxIDs",
	Long: `List taxonomic tree of given taxIDs

Examples:

    $ taxonkit list --ids 9606 -n -r --indent "    "
    9606 [species] Homo sapiens
        63221 [subspecies] Homo sapiens neanderthalensis
        741158 [subspecies] Homo sapiens subsp. 'Denisova'

    $ taxonkit list --ids 9606 --indent ""
    9606
    63221
    741158

`,
	Run: func(cmd *cobra.Command, args []string) {
		config := getConfigs(cmd)
		runtime.GOMAXPROCS(config.Threads)

		ids := getFlagTaxonIDs(cmd, "ids")
		indent := getFlagString(cmd, "indent")
		jsonFormat := getFlagBool(cmd, "json")

		files := getFileList(args)
		if len(files) > 1 || (len(files) == 1 && files[0] == "stdin") {
			log.Warningf("no positional arguments needed")
		}

		outfh, err := xopen.Wopen(config.OutFile)
		checkError(err)

		printName := getFlagBool(cmd, "show-name")
		printRank := getFlagBool(cmd, "show-rank")

		// -------------------- load data ----------------------

		var names map[int32]string
		var delnodes map[int32]struct{}
		var merged map[int32]int32
		var tree map[int32]map[int32]bool // different from that in lineage.go
		var ranks map[int32]string

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			_, _, names, delnodes, merged = loadData(config, false, false)
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			tree = make(map[int32]map[int32]bool, mapInitialSize)
			ranks = make(map[int32]string, mapInitialSize)

			fh, err := xopen.Ropen(config.NodesFile)
			checkError(err)

			items := make([]string, 6)
			scanner := bufio.NewScanner(fh)
			var _child, _parent int
			var child, parent int32
			var rank string
			var ok bool
			for scanner.Scan() {
				stringSplitN(scanner.Text(), "\t", 6, &items)
				if len(items) < 6 {
					continue
				}

				_child, err = strconv.Atoi(items[0])
				if err != nil {
					continue
				}

				_parent, err = strconv.Atoi(items[2])
				if err != nil {
					continue
				}
				child, parent, rank = int32(_child), int32(_parent), items[4]

				// ----------------------------------

				if _, ok = tree[parent]; !ok {
					tree[parent] = make(map[int32]bool)
				}
				tree[parent][child] = false
				if _, ok = tree[child]; !ok {
					tree[child] = make(map[int32]bool)
				}
				if printRank {
					ranks[child] = rank
				}
			}
			if err := scanner.Err(); err != nil {
				checkError(err)
			}
			wg.Done()
		}()

		wg.Wait()

		// -------------------- load data ----------------------

		var level int
		if jsonFormat {
			outfh.WriteString("{\n")
		}
		var newtaxid int32
		var child int32
		for i, id := range ids {
			if _, ok := tree[int32(id)]; !ok {
				// check if it was deleted
				if _, ok = delnodes[int32(id)]; ok {
					log.Warningf("taxid %d was deleted", child)
					continue
				}
				// check if it was merged
				if newtaxid, ok = merged[int32(id)]; ok {
					log.Warningf("taxid %d was merged into %d", child, newtaxid)
					id = int(newtaxid)
				} else {
					log.Warningf("taxid %d not found", child)
					continue
				}
			}

			level = 0
			if jsonFormat {
				level = 1
			}

			outfh.WriteString(strings.Repeat(indent, level))

			if jsonFormat {
				outfh.WriteString(`"`)
			}
			outfh.WriteString(fmt.Sprintf("%d", id))

			if printRank {
				outfh.WriteString(fmt.Sprintf(" [%s]", ranks[int32(id)]))
			}
			if printName {
				outfh.WriteString(fmt.Sprintf(" %s", names[int32(id)]))
			}

			level = 0
			if jsonFormat {
				outfh.WriteString(`": {`)
				level = 1
			}
			outfh.WriteString("\n")
			if config.LineBuffered {
				outfh.Flush()
			}

			traverseTree(tree, int32(id), outfh, indent, level+1, names,
				printName, ranks, printRank, jsonFormat, config)

			if jsonFormat {
				outfh.WriteString(fmt.Sprintf("%s}", strings.Repeat(indent, level)))
			}
			if jsonFormat && i < len(ids)-1 {
				outfh.WriteString(",")
			}
			outfh.WriteString("\n")
			if config.LineBuffered {
				outfh.Flush()
			}
		}

		if jsonFormat {
			outfh.WriteString("}\n")
			if config.LineBuffered {
				outfh.Flush()
			}
		}

		defer outfh.Close()
	},
}

func init() {
	RootCmd.AddCommand(listCmd)

	listCmd.Flags().StringP("ids", "", "", "taxID(s), multiple values should be separated by comma")
	listCmd.Flags().StringP("indent", "", "  ", "indent")
	listCmd.Flags().BoolP("show-rank", "r", false, `output rank`)
	listCmd.Flags().BoolP("show-name", "n", false, `output scientific name`)
	listCmd.Flags().BoolP("json", "", false, `output in JSON format. you can save the result in file with suffix ".json" and open with modern text editor`)
}

func traverseTree(tree map[int32]map[int32]bool, parent int32,
	outfh *xopen.Writer, indent string, level int,
	names map[int32]string, printName bool,
	ranks map[int32]string, printRank bool,
	jsonFormat bool, config Config) {
	if _, ok := tree[parent]; !ok {
		return
	}

	// sort children by taxid
	children := make([]int, len(tree[parent]))
	i := 0
	for child := range tree[parent] {
		children[i] = int(child)
		i++
	}
	sort.Ints(children)

	var child int32
	for i, c := range children {
		child = int32(c)
		if tree[parent][child] {
			continue
		}

		outfh.WriteString(strings.Repeat(indent, level))

		if jsonFormat {
			outfh.WriteString(`"`)
		}
		outfh.WriteString(fmt.Sprintf("%d", child))
		if printRank {
			outfh.WriteString(fmt.Sprintf(" [%s]", ranks[child]))
		}
		if printName {
			outfh.WriteString(fmt.Sprintf(" %s", names[child]))
		}

		var ok bool
		if jsonFormat {
			_, ok = tree[child]
			if ok {
				outfh.WriteString(`": {`)
			} else {
				outfh.WriteString(`": {}`)
				if i < len(children)-1 {
					outfh.WriteString(",")
				}
			}
		}
		outfh.WriteString("\n")
		if config.LineBuffered {
			outfh.Flush()
		}

		tree[parent][child] = true

		traverseTree(tree, child, outfh, indent, level+1, names, printName,
			ranks, printRank, jsonFormat, config)

		if jsonFormat && ok {
			outfh.WriteString(fmt.Sprintf("%s}", strings.Repeat(indent, level)))
			if level > 1 && i < len(children)-1 {
				outfh.WriteString(",")
			}
			outfh.WriteString("\n")
			if config.LineBuffered {
				outfh.Flush()
			}
		}
	}
}
