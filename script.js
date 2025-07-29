let currentGroupIdx = 0;
let totalGroups = 0;
let navigationDirection = 'next'; // Track direction: 'next' or 'prev'

function deleteImage(filePath, wrapper) {
    fetch('/api/delete', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({ path: filePath })
    })
    .then(res => res.json())
    .then(data => {
        if (data.success) {
            // Remove the image from the UI
            wrapper.style.transition = 'opacity 0.3s';
            wrapper.style.opacity = '0';
            setTimeout(() => {
                wrapper.remove();
                // Check if this was the last image, and if so, skip to next valid group
                const remainingImages = document.querySelectorAll('.image-wrapper').length;
                if (remainingImages <= 1) {
                    // Only one or no images left, skip to next valid group
                    navigateToValidGroup('next');
                }
            }, 300);
        }
        // No alerts - silent operation
    })
    .catch(err => {
        // Silent failure - no alerts
        console.error('Error deleting file:', err);
    });
}

function fetchGroup(idx, callback) {
    fetch(`/api/group?idx=${idx}`)
        .then(res => {
            if (!res.ok) {
                // Group doesn't exist, has no images, or error
                console.log(`Group ${idx} not found or has no valid images (status: ${res.status})`);
                if (callback) callback(null);
                return;
            }
            return res.json();
        })
        .then(data => {
            if (data) {
                totalGroups = totalGroups || 1; // fallback
                if (callback) callback(data);
                else renderGroup(data, idx);
            } else if (callback) {
                callback(null);
            }
        })
        .catch(err => {
            console.error('Error fetching group:', err);
            if (callback) callback(null);
        });
}

function navigateToValidGroup(direction) {
    navigationDirection = direction;
    
    function tryGroup(idx, searchLimit = 1000) {
        if (idx < 0) {
            // Went too far back, try going forward instead
            if (direction === 'prev') {
                navigateToValidGroup('next');
            } else {
                // No valid groups found at all
                currentGroupIdx = -1;
                document.getElementById('images-grid').innerHTML = '<div style="text-align: center; padding: 40px;"><h2>🎉 All Done!</h2><p>No more duplicate groups found with multiple images.</p></div>';
                document.getElementById('group-score').textContent = 'Deduplication complete!';
            }
            return;
        }
        
        if (idx >= searchLimit) {
            // Reached search limit going forward
            if (direction === 'next') {
                // Try going backwards instead
                if (currentGroupIdx > 0) {
                    navigateToValidGroup('prev');
                } else {
                    document.getElementById('images-grid').innerHTML = '<div style="text-align: center; padding: 40px;"><h2>🎉 All Done!</h2><p>No more duplicate groups found with multiple images.</p></div>';
                    document.getElementById('group-score').textContent = 'Deduplication complete!';
                }
            } else {
                // Going backwards and hit the limit, try going forward
                navigateToValidGroup('next');
            }
            return;
        }
        
        fetchGroup(idx, (data) => {
            if (!data) {
                // This group doesn't exist or has no valid images, continue searching
                console.log(`Group ${idx} not available, continuing search...`);
                const nextIdx = direction === 'next' ? idx + 1 : idx - 1;
                tryGroup(nextIdx, searchLimit);
                return;
            }
            
            if (data.images && data.images.length > 1) {
                // Found a valid group with multiple images
                currentGroupIdx = idx;
                renderGroup(data, idx);
            } else {
                // This group has only one image, skip it
                console.log(`Group ${idx} has only ${data.images ? data.images.length : 0} images, skipping...`);
                const nextIdx = direction === 'next' ? idx + 1 : idx - 1;
                tryGroup(nextIdx, searchLimit);
            }
        });
    }
    
    const startIdx = direction === 'next' ? currentGroupIdx + 1 : currentGroupIdx - 1;
    tryGroup(startIdx);
}

function renderGroup(data, idx) {
    document.getElementById('group-score').textContent = `Group ${idx + 1}: Similarity Score ${data.group_similarity_score.toFixed(2)} (${data.images.length} images)`;
    const grid = document.getElementById('images-grid');
    grid.innerHTML = '';
    
    // Determine sizing based on number of images
    const numImages = data.images.length;
    let maxHeight = '';
    if (numImages > 2) {
        maxHeight = '50vh'; // Limit to 50% of viewport height for 3+ images
    }
    
    data.images.forEach((img, i) => {
        const wrapper = document.createElement('div');
        wrapper.className = 'image-wrapper';
        wrapper.style.position = 'relative';
        wrapper.style.display = 'inline-block';
        wrapper.style.margin = '10px';
        wrapper.style.width = '45%'; // Max 50% width minus margins for side-by-side viewing
        wrapper.style.maxWidth = '45vw'; // Ensure it never exceeds 50% of viewport width
        wrapper.style.verticalAlign = 'top'; // Align images to top when side by side
        
        // Image
        const image = document.createElement('img');
        image.src = '/images/' + img.path.replace(/^\/+/, '');
        image.style.width = '100%';
        image.style.height = 'auto';
        image.style.display = 'block';
        image.style.border = '2px solid #ccc';
        
        // Calculate max height to leave room for header, footer, and EXIF data
        // Assume: top bar ~60px, footer ~60px, EXIF data ~200px, margins ~80px
        const maxImageHeight = 'calc(100vh - 400px)';
        image.style.maxHeight = maxImageHeight;
        image.style.objectFit = 'contain'; // Maintain aspect ratio
        // Trash icon
        const trash = document.createElement('div');
        trash.innerHTML = '✖'; // X mark that can definitely be colored red
        trash.style.cssText = `
            position: absolute !important;
            top: 10px !important;
            right: 10px !important;
            font-size: 3em !important;
            font-weight: bold !important;
            color: #ff0000 !important;
            background-color: rgba(255, 255, 255, 0.95) !important;
            border-radius: 50% !important;
            padding: 12px !important;
            border: 3px solid #ff0000 !important;
            box-shadow: 0 2px 8px rgba(0,0,0,0.5) !important;
            cursor: pointer !important;
            z-index: 1000 !important;
            transition: transform 0.2s !important;
            width: 50px !important;
            height: 50px !important;
            display: flex !important;
            align-items: center !important;
            justify-content: center !important;
            line-height: 1 !important;
        `;
        trash.title = "Delete this image";
        trash.onmouseover = () => {
            trash.style.transform = 'scale(1.1)';
            trash.style.backgroundColor = 'rgba(255, 0, 0, 0.1)';
        };
        trash.onmouseout = () => {
            trash.style.transform = 'scale(1)';
            trash.style.backgroundColor = 'rgba(255, 255, 255, 0.95)';
        };
        trash.onclick = () => {
            const fullPath = img.original_path || img.path;
            deleteImage(fullPath, wrapper);
        };
        // Image click logs original path
        image.onclick = () => {
            console.log('Image clicked:', img.original_path || img.path);
        };
        // Info
        const info = document.createElement('div');
        info.style.textAlign = 'center';
        info.style.marginTop = '8px';
        let infoHtml = '';
        // Filename with extension
        const filename = (img.original_path || img.path).split('/').pop();
        infoHtml += `<div style='color:#444;font-size:1em;font-weight:bold;margin-bottom:4px;'>${filename}</div>`;
        // Subject (should be EXIF Subject, not ImageDescription)
        if (img.subject) {
            infoHtml += `<div style='color:#333;font-size:1.1em;font-weight:bold;'>Subject: ${img.subject}</div>`;
        }
        // Date Taken
        if (img.date_taken) {
            infoHtml += `<div style='color:#666;font-size:1em;'>Date Taken: ${img.date_taken}</div>`;
        }
        // Dimensions and size
        infoHtml += `<div style='font-size:1.05em;'>${img.width} × ${img.height} px`;
        if (img.size) {
            let sizeMB = (img.size / (1024*1024)).toFixed(2);
            infoHtml += ` &nbsp;•&nbsp; ${sizeMB} MB`;
        }
        infoHtml += '</div>';
        // Camera Make and Model
        if (img.camera_make || img.camera_model) {
            let makeModel = [img.camera_make, img.camera_model].filter(Boolean).join(' ');
            infoHtml += `<div style='color:#666;font-size:0.95em;'>${makeModel}</div>`;
        }
        if (!img.has_exif) {
            infoHtml += `<div style='color:red;'>EXIF DATA MISSING</div>`;
        }
        info.innerHTML = infoHtml;
        wrapper.appendChild(image);
        wrapper.appendChild(info);
        wrapper.appendChild(trash);
        grid.appendChild(wrapper);
    });
}

function dedupeGroup() {
    fetchGroup(currentGroupIdx, (data) => {
        if (!data || data.images.length <= 1) {
            // Already processed or no images, move to next valid group
            navigateToValidGroup('next');
            return;
        }
        
        // Sort images by score (highest first)
        const sortedImages = data.images.sort((a, b) => b.score - a.score);
        
        // Keep the best image (highest score), delete the rest
        const imagesToDelete = sortedImages.slice(1); // All except the first (best) one
        
        if (imagesToDelete.length === 0) {
            // Only one image, move to next group
            navigateToValidGroup('next');
            return;
        }
        
        let deletedCount = 0;
        const totalToDelete = imagesToDelete.length;
        
        // Delete images one by one
        imagesToDelete.forEach((img, index) => {
            const fullPath = img.original_path || img.path;
            fetch('/api/delete', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ path: fullPath })
            })
            .then(res => res.json())
            .then(deleteData => {
                if (deleteData.success) {
                    console.log(`Deleted: ${fullPath}`);
                    deletedCount++;
                    // After all deletions complete, move to next valid group
                    if (deletedCount === totalToDelete) {
                        navigateToValidGroup('next');
                    }
                }
            })
            .catch(err => {
                console.error('Error deleting file:', err);
                deletedCount++;
                if (deletedCount === totalToDelete) {
                    navigateToValidGroup('next');
                }
            });
        });
    });
}

document.getElementById('prev-group').onclick = () => {
    navigateToValidGroup('prev');
};

document.getElementById('dedupe-button').onclick = () => {
    dedupeGroup();
};

document.getElementById('next-group').onclick = () => {
    navigateToValidGroup('next');
};

window.onload = () => {
    // Start by checking the first group (index 0)
    currentGroupIdx = -1; // Start at -1 so navigateToValidGroup('next') will check index 0
    navigateToValidGroup('next');
};