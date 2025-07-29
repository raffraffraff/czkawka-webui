# [UNOFFICIAL] Web UI for manual image de-duplication
Ever had to deduplicate a *lot* of images? Well, [czkawka](https://github.com/qarmin/czkawka) is a greate tools for doing this. It has both CLI and GUI versions. However, I have personally found that the GUI version is confusing and tends to hang frequently on my laptop. And while the CLI version is amazing for batch operations, if you are using one of the lower-similarity settings, you definitely need to 'eyeball' the images and make your own personal selection based on EXIF data, size, format etc. This web UI hopes to bridge the gap.

# Usage
## Step 1: Generate JSON file with czkawka
You need to download and run `czkawka_cli`. There's some strategy required here... The first thing you might do is run a first pass on your images using the First thing you need to do is run it with this type of configuration:

```
czkawka_cli image \
  --directories /path/to/images \
  --similarity-preset VeryHigh \
  --hash-alg VertGradient \
  --image-filter Lanczos3 \
  --pretty-file-to-save duplicates.json
```

This command can take a _long_ time to run, because it recursively checks every image under /path/to/images, uses Lanczos3 resampling to resize, convert and reduce your images and then hash each one with VertGradient. It caches this set of hashes (on Linux, it'll probably be under ~/.cache/czkawka if you need to nuke it). It also generates a JSON file that contains each group of similar images. _This is the file we will use!_

## Step 2: Build and run this program!
First, clone this repo and build `czkawka-web` (you'll obviously need Golang installed!):
```
git clone https://github.com/raffraffraff/czkawka-webui.git
cd czkawka-webgui
go build -ldflags="-s -w" -o czkawka-web dupe_delete.go
```

Now, run the web UI, pointing it to your images and to the duplicates.json file:
```
./czkawka-web \
  -imagepath /path/to/images \
  -duplicates /path/to/duplicates.json \
  -port 8080
```

## Step 3: Nuke your duplicates!
Don't worry, this program does _nothing_ without your say-so. I wrote it because I was paranoid about letting a CLI delete my images without showing me, side-by-side, what the options were. You should see a pretty simple web interface that lets you:
1. Navigate between groups of similar images (read from the duplicates.json)
2. View the images side-by-side with:
   - EXIF original / creation date
   - EXIF subject
   - File name (including extension)
3. Selectively delete images
4. Automatically 'DE-DUPE!' based on built-in rules

# How it works
The real work is carried out by `czkawka_cli`. What this web UI does is:
1. Host a local website for navigating and de-duplicating
2. Automatically generate JPG previews of .CR2 images so the browser can show them
3. Skip image groups that have only 1 image
   - Effectively gives you 'auto-proceed' once you dedupe
   - Lets you 'continue' after deleting a bunch of images and retarting the web UI

In reality, you might find that with huge collections this whole process takes a LONG time. I'm sorry, but that's reality. If you find that after, say 200 images matches you find that your particular `czkawka_cli` settings are paranoid enough to avoid accidental non-duplicates, you can go back and rerun that command with a deletion strategy like "All Except Oldest" (ie: `--delete-method AEO`), and then rerun the whole hashing process with less paranoid settings, like:

```
czkawka_cli image \
  --directories /path/to/images \
  --similarity-preset Medium \
  --hash-alg VertGradient \
  --image-filter Lanczos3 \
  --pretty-file-to-save duplicates.json
```

Whenever you regenerate the duplicates.json file, make sure you stop/start this web UI and clear browser cache (usually CTRL + SHIFT + R)

# DISCLAIMER
Yeah this should probably be right at the top but... DO NOT TRUST ANY SOFTWARE WITH YOUR ORIGINALS!!! Make a copy of your images and use these programs on the copies. While I have tried to make this program very safe to use (on my own personal photo collection!) I am not responsibile for loss of your images through any bug, accident or misuse. USE WITH A COPY OF YOUR PHOTOS, FOR THE LOVE OF ...
